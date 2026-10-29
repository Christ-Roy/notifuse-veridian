package mailbox

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Réglages de lecture. Le repère est le dernier UID traité par boîte : arrêt ou panne,
// la lecture reprend à l'UID suivant, rien n'est sauté ni relu.
const (
	// backfillWindow : au premier branchement d'une boîte, on remonte ce délai.
	backfillWindow = 120 * 24 * time.Hour
	// maxPerRun : messages traités par boîte et par passage (le reste au passage suivant).
	maxPerRun = 400
	// batchBytes / batchCount : bornes de mémoire d'un lot de lecture.
	batchBytes = 24 << 20
	batchCount = 25
	// runBudget : temps maximal d'un passage sur une boîte.
	runBudget = 100 * time.Second
	// historyBatch : envois repris de message_history par passage et par workspace.
	historyBatch = 150
)

// Syncer lit les boîtes IMAP des relais (rattrapage par UID) et reprend l'historique envoyé.
// Il est appelé par le poller IMAP existant : aucun daemon ni tâche planifiée de plus.
type Syncer struct {
	store    *Store
	ingest   *Ingestor
	dialer   Dialer
	log      logger.Logger
	renderer Renderer

	mu      sync.Mutex
	running map[string]bool
}

// NewSyncer construit le syncer. renderer peut être nil (reprise sans re-rendu).
func NewSyncer(store *Store, ingest *Ingestor, dialer Dialer, renderer Renderer, log logger.Logger) *Syncer {
	return &Syncer{store: store, ingest: ingest, dialer: dialer, renderer: renderer, log: log, running: map[string]bool{}}
}

func (s *Syncer) acquire(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[key] {
		return false
	}
	s.running[key] = true
	return true
}

func (s *Syncer) release(key string) {
	s.mu.Lock()
	delete(s.running, key)
	s.mu.Unlock()
}

// BoxFor retrouve le profil lié à une boîte IMAP d'un workspace.
func BoxFor(ws *domain.Workspace, integ *domain.Integration) BoxInfo {
	b := BoxInfo{IntegrationID: integ.ID}
	if integ.IMAPSettings != nil {
		b.Address = strings.ToLower(integ.IMAPSettings.Username)
		b.Folder = integ.IMAPSettings.GetFolder()
	}
	for i := range ws.Integrations {
		p := &ws.Integrations[i]
		if p.Type != domain.IntegrationTypeEmail || p.EmailProvider.VeridianReturnIMAPIntegrationID != integ.ID {
			continue
		}
		b.ProfileID, b.ProfileName = p.ID, p.Name
		for _, snd := range p.EmailProvider.Senders {
			if snd.IsDefault || b.Address == "" {
				if snd.Email != "" {
					b.Address = strings.ToLower(snd.Email)
				}
			}
		}
		break
	}
	return b
}

// SyncBox lit les nouveaux messages d'une boîte. Ne panique jamais, ne rend rien : les erreurs
// sont consignées dans le repère de la boîte (visibles par mailboxes.list).
func (s *Syncer) SyncBox(ctx context.Context, ws *domain.Workspace, integ *domain.Integration) {
	if ws == nil || integ == nil || integ.IMAPSettings == nil {
		return
	}
	key := ws.ID + "|" + integ.ID
	if !s.acquire(key) {
		return
	}
	defer s.release(key)
	defer func() {
		if r := recover(); r != nil {
			s.log.WithFields(map[string]interface{}{"workspace_id": ws.ID, "integration_id": integ.ID, "panic": r}).Error("Mailbox: sync panicked, recovered")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, runBudget)
	defer cancel()

	box := BoxFor(ws, integ)
	folder := integ.IMAPSettings.GetFolder()
	log := s.log.WithFields(map[string]interface{}{"workspace_id": ws.ID, "integration_id": integ.ID})

	state, err := s.store.GetSync(ctx, ws.ID, integ.ID, folder)
	if err != nil {
		log.WithField("error", err.Error()).Warn("Mailbox: read sync state failed")
		return
	}
	var lastUID, validity uint32
	var total int
	if state != nil {
		lastUID, validity = state.LastUID, state.UIDValidity
	}

	fail := func(msg string) {
		if e := s.store.SaveSync(context.Background(), ws.ID, integ.ID, folder, validity, lastUID, "", false, msg, total); e != nil {
			log.WithField("error", e.Error()).Warn("Mailbox: save sync state failed")
		}
		log.WithField("error", msg).Warn("Mailbox: sync failed")
	}

	sess, err := s.dialer.Dial(ctx, integ.IMAPSettings)
	if err != nil {
		fail(err.Error())
		return
	}
	defer func() { _ = sess.Close() }()

	var since time.Time
	if state == nil || state.UIDValidity != sess.UIDValidity() {
		// Premier branchement ou UIDVALIDITY changé : tout relire dans la fenêtre (dédoublonné par Message-ID).
		lastUID = 0
		since = time.Now().Add(-backfillWindow)
	}
	validity = sess.UIDValidity()

	infos, err := sess.ListUIDs(ctx, lastUID, since)
	if err != nil {
		fail(err.Error())
		return
	}
	if len(infos) > maxPerRun {
		infos = infos[:maxPerRun]
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].UID < infos[j].UID })

	for len(infos) > 0 {
		// Lot borné en nombre et en octets.
		n, bytes := 0, int64(0)
		for n < len(infos) && n < batchCount && (n == 0 || bytes+min64(infos[n].Size, MaxRawBytes) <= batchBytes) {
			bytes += min64(infos[n].Size, MaxRawBytes)
			n++
		}
		batch := infos[:n]
		infos = infos[n:]
		uids := make([]uint32, len(batch))
		for i, b := range batch {
			uids[i] = b.UID
		}
		raws, err := sess.FetchRaw(ctx, uids)
		if err != nil {
			fail(err.Error())
			return
		}
		byUID := map[uint32]RawMessage{}
		for _, r := range raws {
			byUID[r.UID] = r
		}
		for _, uid := range uids {
			r, ok := byUID[uid]
			if !ok || len(r.Raw) == 0 {
				// message disparu entre la liste et la lecture (supprimé) : on avance.
				lastUID = uid
				continue
			}
			ref := MailboxRef{IntegrationID: integ.ID, Folder: folder, UIDValidity: validity, UID: uid}
			if _, inserted, err := s.ingest.IngestRaw(ctx, ws.ID, box, ref, r.Raw, r.InternalDate, r.Truncated); err != nil {
				// On s'arrête AVANT ce message : il sera relu au prochain passage.
				if e := s.store.SaveSync(context.Background(), ws.ID, integ.ID, folder, validity, lastUID, "", false, err.Error(), total); e != nil {
					log.WithField("error", e.Error()).Warn("Mailbox: save sync state failed")
				}
				log.WithFields(map[string]interface{}{"uid": uid, "error": err.Error()}).Warn("Mailbox: ingest failed, will retry")
				return
			} else if inserted {
				total++
			}
			lastUID = uid
		}
		// Repère enregistré à chaque lot : un arrêt brutal ne perd que le lot en cours.
		if e := s.store.SaveSync(context.Background(), ws.ID, integ.ID, folder, validity, lastUID, "", true, "", 0); e != nil {
			log.WithField("error", e.Error()).Warn("Mailbox: save sync state failed")
			return
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err := s.store.SaveSync(context.Background(), ws.ID, integ.ID, folder, validity, lastUID, "", true, "", total); err != nil {
		log.WithField("error", err.Error()).Warn("Mailbox: save sync state failed")
	}
	if total > 0 {
		log.WithFields(map[string]interface{}{"ingested": total, "last_uid": lastUID}).Info("Mailbox: sync done")
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
