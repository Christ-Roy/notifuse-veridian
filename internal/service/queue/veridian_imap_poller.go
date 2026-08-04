package queue

// Veridian fork — poller IMAP self-service (Lot 1 sprint cold outbound,
// 2026-06-15). BRIQUE FONDATRICE consommée par les lots 2 (bounce-loop) et 3
// (stop-on-reply).
//
// Pattern : goroutine + time.NewTicker, calqué sur
// VeridianIdempotencyCleanupService (cron Veridian existant). À chaque tick :
//   1. List() tous les workspaces.
//   2. Pour chaque workspace, repère les intégrations de type IMAP.
//   3. Pour chaque boîte IMAP : dial (timeout court) -> select -> SEARCH SINCE
//      -> filtre les UID déjà vus (repo durable) -> dispatch aux consumers
//      -> marque les UID vus uniquement si tous les consumers réussissent.
//
// Best-effort de bout en bout :
//   - échec de connexion / login / fetch d'UNE boîte => log + skip cette boîte,
//     on continue les autres et le poller reste vivant.
//   - panic d'un consumer => récupéré, loggué, n'affecte ni les autres
//     consumers ni le poller.
//   - erreur DB sur le repo uid_seen => on SKIP la boîte (on préfère ne rien
//     traiter plutôt que de risquer un double-dispatch).
//   - le poller ne renvoie jamais d'erreur fatale, ne panique jamais, ne bloque
//     jamais le reste de l'app.
//
// Fenêtre SEARCH SINCE : on ne rapatrie que les messages reçus depuis
// `lookbackWindow` (défaut 7j) pour borner le travail sur une grosse boîte au
// premier branchement. L'idempotence durable (uid_seen) garantit qu'un message
// déjà traité dans cette fenêtre n'est jamais re-dispatché.

import (
	"context"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Bornes par défaut du poller.
const (
	// defaultIMAPLookbackWindow : fenêtre SEARCH SINCE. On ne regarde pas plus
	// loin dans le passé que ça (évite de rapatrier des années d'historique).
	defaultIMAPLookbackWindow = 7 * 24 * time.Hour
	// defaultIMAPTickInterval : cadence de la boucle principale du poller. À
	// chaque tick on traite TOUTES les boîtes dont l'intervalle propre est échu.
	// On garde un tick global court ; le throttle par-boîte est géré via
	// PollingIntervalSeconds (lastPolledAt).
	defaultIMAPTickInterval = 30 * time.Second
	// imapPerBoxFetchTimeout : budget temps max d'un cycle complet sur UNE boîte
	// (dial+fetch+dispatch). Au-delà, on coupe et on passe à la suivante.
	imapPerBoxFetchTimeout = 60 * time.Second
	// maxIMAPMessagesPerPoll borne le nombre de messages neufs traités par cycle
	// de polling et par boîte, pour qu'un backlog massif (premier branchement sur
	// une boîte pleine) ne bloque pas le poller ni n'explose la mémoire. Le reste
	// est rattrapé au cycle suivant (les UID traités sont marqués vus).
	maxIMAPMessagesPerPoll = 500
)

// VeridianIMAPPollerService poll les boîtes IMAP configurées et dispatche les
// messages neufs aux consumers enregistrés.
type VeridianIMAPPollerService struct {
	workspaceRepo domain.WorkspaceRepository
	uidSeenRepo   domain.VeridianIMAPUIDSeenRepository
	dialer        veridianIMAPDialer
	logger        logger.Logger

	tickInterval   time.Duration
	lookbackWindow time.Duration

	mu        sync.RWMutex
	consumers []domain.VeridianIMAPConsumer
	// lastPolledAt[workspaceID|integrationID] => dernier poll réussi/tenté, pour
	// respecter l'intervalle propre de chaque boîte.
	lastPolledAt map[string]time.Time
}

// NewVeridianIMAPPollerService crée le poller de production. tickInterval et
// lookbackWindow <= 0 => valeurs par défaut.
func NewVeridianIMAPPollerService(
	workspaceRepo domain.WorkspaceRepository,
	uidSeenRepo domain.VeridianIMAPUIDSeenRepository,
	log logger.Logger,
	tickInterval time.Duration,
	lookbackWindow time.Duration,
) *VeridianIMAPPollerService {
	if tickInterval <= 0 {
		tickInterval = defaultIMAPTickInterval
	}
	if lookbackWindow <= 0 {
		lookbackWindow = defaultIMAPLookbackWindow
	}
	return &VeridianIMAPPollerService{
		workspaceRepo:  workspaceRepo,
		uidSeenRepo:    uidSeenRepo,
		dialer:         newEmersionIMAPDialer(),
		logger:         log,
		tickInterval:   tickInterval,
		lookbackWindow: lookbackWindow,
		lastPolledAt:   make(map[string]time.Time),
	}
}

// RegisterConsumer enregistre un handler (lots 2/3). Thread-safe. Idempotent
// par Name() : ré-enregistrer un consumer du même Name() remplace l'ancien.
func (s *VeridianIMAPPollerService) RegisterConsumer(c domain.VeridianIMAPConsumer) {
	if c == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.consumers {
		if existing.Name() == c.Name() {
			s.consumers[i] = c
			return
		}
	}
	s.consumers = append(s.consumers, c)
}

// snapshotConsumers retourne une copie de la liste des consumers (lecture
// concurrente sûre pendant un dispatch).
func (s *VeridianIMAPPollerService) snapshotConsumers() []domain.VeridianIMAPConsumer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.VeridianIMAPConsumer, len(s.consumers))
	copy(out, s.consumers)
	return out
}

// Start lance la goroutine de polling. Retourne immédiatement. S'arrête quand
// ctx est annulé (app.GetShutdownContext()).
//
// No-op si aucun consumer n'est enregistré : poller une boîte IMAP pour
// dispatcher à PERSONNE est du travail inutile (et lance une goroutine qui ne
// sert à rien). Les consumers (lots bounce-loop / stop-on-reply) s'enregistrent
// au câblage app.go AVANT app.Start(), donc le check est déterministe au
// démarrage. Effet de bord propre : en l'absence de lots downstream (ou en
// tests unitaires), aucune goroutine n'est lancée.
func (s *VeridianIMAPPollerService) Start(ctx context.Context) {
	if s.workspaceRepo == nil || s.uidSeenRepo == nil {
		s.logger.Warn("VeridianIMAPPoller: deps nil, scheduler skipped")
		return
	}
	if len(s.snapshotConsumers()) == 0 {
		s.logger.Info("VeridianIMAPPoller: no consumer registered, scheduler skipped")
		return
	}

	go func() {
		// Petit délai de démarrage pour ne pas concurrencer le boot (migrations,
		// warmup connexions) — aligné sur l'esprit des workers (delay 30s) mais
		// plus court car le poller est best-effort.
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			s.logger.Info("VeridianIMAPPoller: shutdown during start delay, not starting")
			return
		}

		s.runOnce(ctx)

		ticker := time.NewTicker(s.tickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.logger.Info("VeridianIMAPPoller: context cancelled, stopping")
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce parcourt tous les workspaces et poll les boîtes IMAP dont l'intervalle
// propre est échu. Best-effort : une erreur sur une boîte n'affecte pas les
// autres.
func (s *VeridianIMAPPollerService) runOnce(ctx context.Context) {
	workspaces, err := s.workspaceRepo.List(ctx)
	if err != nil {
		s.logger.WithField("error", err.Error()).Warn("VeridianIMAPPoller: list workspaces failed")
		return
	}

	now := time.Now().UTC()
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		for i := range ws.Integrations {
			integ := &ws.Integrations[i]
			if integ.Type != domain.IntegrationTypeIMAP {
				continue
			}
			settings := integ.IMAPSettings
			if settings == nil {
				continue
			}
			if !s.dueForPoll(ws.ID, integ.ID, settings, now) {
				continue
			}
			s.setLastPolled(ws.ID, integ.ID, now)
			s.pollBox(ctx, ws.ID, integ.ID, settings)

			if ctx.Err() != nil {
				return
			}
		}
	}
}

// boxKey est la clé d'entrée lastPolledAt.
func boxKey(workspaceID, integrationID string) string {
	return workspaceID + "|" + integrationID
}

// dueForPoll indique si l'intervalle propre de la boîte est échu.
func (s *VeridianIMAPPollerService) dueForPoll(workspaceID, integrationID string, settings *domain.IMAPSettings, now time.Time) bool {
	s.mu.RLock()
	last, ok := s.lastPolledAt[boxKey(workspaceID, integrationID)]
	s.mu.RUnlock()
	if !ok {
		return true
	}
	return now.Sub(last) >= settings.GetPollingInterval()
}

func (s *VeridianIMAPPollerService) setLastPolled(workspaceID, integrationID string, now time.Time) {
	s.mu.Lock()
	s.lastPolledAt[boxKey(workspaceID, integrationID)] = now
	s.mu.Unlock()
}

// pollBox traite UNE boîte : dial -> fetch -> filtre vus -> dispatch -> marque.
// Best-effort, jamais de panic remontante (chaque erreur est loggée + skip).
func (s *VeridianIMAPPollerService) pollBox(parentCtx context.Context, workspaceID, integrationID string, settings *domain.IMAPSettings) {
	log := s.logger.WithFields(map[string]interface{}{
		"workspace_id":   workspaceID,
		"integration_id": integrationID,
		"imap_host":      settings.Host,
		"folder":         settings.GetFolder(),
	})

	// Le mot de passe doit avoir été déchiffré par AfterLoad. S'il manque, on
	// skippe (la boîte est mal configurée) sans crasher.
	if settings.Password == "" {
		log.Warn("VeridianIMAPPoller: skip box, password not decrypted")
		return
	}

	ctx, cancel := context.WithTimeout(parentCtx, imapPerBoxFetchTimeout)
	defer cancel()

	client, err := s.dialer.Dial(ctx, settings)
	if err != nil {
		log.WithField("error", err.Error()).Warn("VeridianIMAPPoller: dial failed, skipping box")
		return
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			log.WithField("error", cerr.Error()).Debug("VeridianIMAPPoller: close error")
		}
	}()

	uidValidity := client.UIDValidity()
	folder := settings.GetFolder()
	since := time.Now().UTC().Add(-s.lookbackWindow)

	messages, err := client.FetchSince(ctx, since, maxIMAPMessagesPerPoll)
	if err != nil {
		log.WithField("error", err.Error()).Warn("VeridianIMAPPoller: fetch failed, skipping box")
		return
	}
	if len(messages) == 0 {
		return
	}

	uids := make([]uint32, 0, len(messages))
	for _, m := range messages {
		uids = append(uids, m.UID)
	}

	unseen, err := s.uidSeenRepo.FilterUnseen(ctx, workspaceID, integrationID, folder, uidValidity, uids)
	if err != nil {
		// On préfère ne rien dispatcher que risquer un double-traitement.
		log.WithField("error", err.Error()).Warn("VeridianIMAPPoller: filter unseen failed, skipping box")
		return
	}
	if len(unseen) == 0 {
		return
	}
	unseenSet := make(map[uint32]struct{}, len(unseen))
	for _, u := range unseen {
		unseenSet[u] = struct{}{}
	}

	consumers := s.snapshotConsumers()
	processed := make([]uint32, 0, len(unseen))

	for _, msg := range messages {
		if _, isNew := unseenSet[msg.UID]; !isNew {
			continue
		}
		// Enrichir le DTO avec le contexte workspace/integration.
		msg.WorkspaceID = workspaceID
		msg.IntegrationID = integrationID

		if s.dispatch(consumers, msg, log) {
			processed = append(processed, msg.UID)
		} else {
			log.WithField("uid", msg.UID).Warn("VeridianIMAPPoller: required consumer failed, UID left unseen for retry")
		}
	}

	if len(processed) > 0 {
		if err := s.uidSeenRepo.MarkSeen(ctx, workspaceID, integrationID, folder, uidValidity, processed); err != nil {
			// Échec du marquage : les messages SERONT re-dispatchés au prochain
			// cycle. Les consumers doivent donc être idempotents côté métier
			// (documenté). On log et on continue.
			log.WithFields(map[string]interface{}{
				"error": err.Error(),
				"count": len(processed),
			}).Warn("VeridianIMAPPoller: mark seen failed, messages may be re-dispatched")
			return
		}
		log.WithField("count", len(processed)).Info("VeridianIMAPPoller: dispatched new messages")
	}
}

// dispatch envoie un message à tous les consumers, chacun isolé d'un panic. Il
// retourne true uniquement si tous ont réussi, condition d'acquittement UID.
func (s *VeridianIMAPPollerService) dispatch(consumers []domain.VeridianIMAPConsumer, msg *domain.VeridianIMAPMessage, log logger.Logger) bool {
	succeeded := true
	for _, consumer := range consumers {
		if !s.safeConsume(consumer, msg, log) {
			succeeded = false
		}
	}
	return succeeded
}

// safeConsume appelle un consumer en récupérant tout panic.
func (s *VeridianIMAPPollerService) safeConsume(consumer domain.VeridianIMAPConsumer, msg *domain.VeridianIMAPMessage, log logger.Logger) (succeeded bool) {
	succeeded = true
	defer func() {
		if r := recover(); r != nil {
			succeeded = false
			log.WithFields(map[string]interface{}{
				"consumer": consumer.Name(),
				"panic":    r,
				"uid":      msg.UID,
			}).Error("VeridianIMAPPoller: consumer panicked, recovered")
		}
	}()

	if err := consumer.OnNewMessage(msg); err != nil {
		succeeded = false
		log.WithFields(map[string]interface{}{
			"consumer": consumer.Name(),
			"error":    err.Error(),
			"uid":      msg.UID,
		}).Warn("VeridianIMAPPoller: consumer returned error")
	}
	return succeeded
}
