package activite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// Workspaces : les espaces et leur base.
type Workspaces interface {
	IDs(ctx context.Context) ([]string, error)
	DB(ctx context.Context, workspaceID string) (*sql.DB, error)
}

// Worker : boucle de l'app (pas une tâche planifiée), qui vide la file de chaque espace par lots de 200, ou toutes les 30 s.
type Worker struct {
	WS      Workspaces
	Sender  *Sender
	Log     logger.Logger
	Metrics *Metrics
	// Enabled : l'interrupteur (VERIDIAN_ACTIVITE_ENABLED). Éteint, la file se remplit et se borne, rien ne part.
	Enabled bool
	// EventsURL : l'adresse attendue de l'abonnement (<HUB_BASE_URL>/api/evenements/notifuse) ; le lot part à EventsURL + "/lot".
	// Un abonnement à une autre adresse n'est jamais suivi : l'adresse vient de la configuration.
	EventsURL string
	// Only : si non vide, seuls ces espaces envoient (VERIDIAN_ACTIVITE_WORKSPACES) ; les autres remplissent et bornent leur file.
	// Sert à ouvrir l'envoi à une org d'épreuve avant tout le monde, comme le Hub ouvre /lot aux seules orgs de test.
	Only map[string]bool

	Tick       time.Duration // 10 s
	Settle     time.Duration // 10 s : la capture de l'envoyé et l'hôte du clic ont le temps d'arriver
	FlushEvery time.Duration // 30 s
	MaxLots    int           // lots au plus par espace et par passage (25)
	Bounds     Bounds
	PurgeEvery time.Duration // 10 min
	AlertAfter time.Duration // retard au-delà duquel on alerte (15 min)
	StuckAfter time.Duration // un Hub qui ne prend rien depuis plus longtemps (1 h) : journal error
	Now        func() time.Time

	mu     sync.Mutex
	settle time.Duration
	state  map[string]*wsState
	alert  time.Time
}

type wsState struct {
	lastFlush, pauseUntil, firstFailure, lastPurge, lastLog time.Time
	failures                                                int
	lastVerdict                                             Verdict
	deniedSecret                                            string // empreinte du secret refusé (401) : un nouveau secret relance aussitôt
}

func (w *Worker) defaults() {
	if w.Tick <= 0 {
		w.Tick = 10 * time.Second
	}
	w.settle = w.Settle // négatif : aucun délai (épreuves) ; zéro : 10 s
	if w.Settle < 0 {
		w.settle = 0
	} else if w.Settle == 0 {
		w.settle = 10 * time.Second
	}
	if w.FlushEvery <= 0 {
		w.FlushEvery = 30 * time.Second
	}
	if w.MaxLots <= 0 {
		w.MaxLots = 25
	}
	if w.Bounds == (Bounds{}) {
		w.Bounds = DefaultBounds
	}
	if w.PurgeEvery <= 0 {
		w.PurgeEvery = 10 * time.Minute
	}
	if w.AlertAfter <= 0 {
		w.AlertAfter = 15 * time.Minute
	}
	if w.StuckAfter <= 0 {
		w.StuckAfter = time.Hour
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Metrics == nil {
		w.Metrics = NewMetrics()
	}
	if w.Sender == nil {
		w.Sender = &Sender{}
	}
	if w.state == nil {
		w.state = map[string]*wsState{}
	}
}

// Run tourne jusqu'à l'annulation de ctx.
func (w *Worker) Run(ctx context.Context) {
	w.mu.Lock()
	w.defaults()
	w.mu.Unlock()
	w.Metrics.SetEnabled(w.Enabled)
	t := time.NewTicker(w.Tick)
	defer t.Stop()
	for {
		w.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Pass : un passage sur tous les espaces.
func (w *Worker) Pass(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.defaults()
	defer func() {
		if r := recover(); r != nil {
			w.Log.WithField("panic", r).Error("Activité Mail : passage interrompu, repris au suivant")
		}
	}()
	ids, err := w.WS.IDs(ctx)
	if err != nil {
		w.Log.WithField("error", err.Error()).Warn("Activité Mail : espaces illisibles")
		return
	}
	now := w.Now()
	var agg passStats
	for _, ws := range ids {
		if ctx.Err() != nil {
			return
		}
		w.workspace(ctx, ws, now, &agg)
	}
	w.Metrics.SetPass(agg.pending, agg.withPending, agg.failing, agg.oldestAge(now), now)
	late := w.Enabled && agg.oldestAge(now) > w.AlertAfter
	w.Metrics.SetAlert(late)
	if late && now.Sub(w.alert) >= 10*time.Minute {
		w.alert = now
		w.Log.WithFields(map[string]interface{}{"pending": agg.pending, "oldest_age_s": int(agg.oldestAge(now).Seconds()),
			"workspaces": agg.withPending, "failing": agg.failing}).Error("Activité Mail : retard de la file vers le Hub")
	} else if !late && !w.alert.IsZero() {
		w.alert = time.Time{}
		w.Log.Info("Activité Mail : retard résorbé")
	}
}

type passStats struct {
	pending, withPending, failing int
	oldest                        time.Time
}

func (p *passStats) oldestAge(now time.Time) time.Duration {
	if p.oldest.IsZero() {
		return 0
	}
	return now.Sub(p.oldest)
}

func (w *Worker) workspace(ctx context.Context, ws string, now time.Time, agg *passStats) {
	db, err := w.WS.DB(ctx, ws)
	if err != nil || db == nil {
		return
	}
	st := w.state[ws]
	if st == nil {
		st = &wsState{}
		w.state[ws] = st
	}
	log := w.Log.WithField("workspace_id", ws)
	if now.Sub(st.lastPurge) >= w.PurgeEvery {
		st.lastPurge = now
		expired, overflow, err := Purge(ctx, db, w.Bounds)
		if err != nil {
			if !isMissingTable(err) {
				log.WithField("error", err.Error()).Warn("Activité Mail : bornes de la file non tenues ce passage")
			}
			return
		}
		w.Metrics.AddEvents(OutcomeExpired, expired)
		w.Metrics.AddEvents(OutcomeOverflow, overflow)
		if overflow > 0 {
			log.WithField("dropped", overflow).Error("Activité Mail : file pleine, les plus vieux événements sont abandonnés")
		}
	}
	stats, err := ReadStats(ctx, db, w.settle)
	if err != nil {
		return // base sans la file (migration en retard) : rien à faire
	}
	if stats.Pending > 0 {
		agg.pending += stats.Pending
		agg.withPending++
		if agg.oldest.IsZero() || stats.Oldest.Before(agg.oldest) {
			agg.oldest = stats.Oldest
		}
	}
	if st.failures > 0 {
		agg.failing++
	}
	if !w.Enabled || stats.Ready == 0 || (len(w.Only) > 0 && !w.Only[ws]) {
		return
	}
	sub, found, err := ReadSubscription(ctx, db)
	if err != nil || !found || !sub.Enabled {
		return // espace délié du Hub : la file se périme seule
	}
	if now.Before(st.pauseUntil) && !(st.lastVerdict == Unauthorized && fingerprint(sub.Secret) != st.deniedSecret) {
		return
	}
	if stats.Ready < MaxEvents && now.Sub(st.lastFlush) < w.FlushEvery && st.failures == 0 {
		return
	}
	if sub.URL != w.EventsURL {
		if now.Sub(st.lastLog) >= time.Hour {
			st.lastLog = now
			log.Warn("Activité Mail : l'abonnement du Hub ne pointe pas l'adresse configurée, rien n'est envoyé")
		}
		return
	}
	st.lastFlush = now
	for i := 0; i < w.MaxLots; i++ {
		rows, orphans, err := ReadReady(ctx, db, w.settle, MaxEvents)
		if err != nil {
			log.WithField("error", err.Error()).Warn("Activité Mail : file illisible")
			return
		}
		if err := MarkDone(ctx, db, orphans, OutcomeOrphan, ""); err == nil {
			w.Metrics.AddEvents(OutcomeOrphan, len(orphans))
		}
		if len(rows) == 0 {
			return
		}
		for _, b := range Split(ws, rows, MaxEvents, MaxBytes) {
			if !w.deliver(ctx, db, ws, sub, b, st, log) {
				return
			}
		}
		if len(rows)+len(orphans) < MaxEvents {
			return
		}
	}
}

// deliver poste un lot et applique le verdict. false = arrêter l'espace pour ce passage.
func (w *Worker) deliver(ctx context.Context, db *sql.DB, ws string, sub Subscription, b Batch, st *wsState, log logger.Logger) bool {
	res := w.Sender.Send(ctx, w.EventsURL+"/lot", sub.Secret, b)
	now := w.Now()
	w.Metrics.AddLot(res.Verdict)
	st.lastVerdict = res.Verdict
	f := map[string]interface{}{"lot_id": b.Lot.LotID, "events": len(b.IDs), "bytes": len(b.Body), "status": res.Status}
	switch res.Verdict {
	case Accepted:
		if err := MarkDone(ctx, db, b.IDs, OutcomeAccepted, b.Lot.LotID); err != nil {
			log.WithFields(f).WithField("error", err.Error()).Error("Activité Mail : lot accepté mais non clos, il sera rejoué (le Hub dédoublonne)")
			return false
		}
		w.Metrics.AddEvents("accepte", res.Reply.Accepted)
		w.Metrics.AddEvents("doublon", res.Reply.Duplicates)
		w.Metrics.AddEvents("ignore", len(res.Reply.Ignored))
		if st.failures > 0 {
			log.WithFields(f).WithField("after_failures", st.failures).Info("Activité Mail : le Hub reprend les lots")
		}
		st.failures, st.firstFailure, st.pauseUntil, st.deniedSecret = 0, time.Time{}, time.Time{}, ""
		f["acceptes"], f["doublons"], f["ignores"] = res.Reply.Accepted, res.Reply.Duplicates, len(res.Reply.Ignored)
		log.WithFields(f).Info("Activité Mail : lot remis au Hub")
		return true
	case Refused:
		_ = MarkDone(ctx, db, b.IDs, OutcomeRefused, b.Lot.LotID)
		w.Metrics.AddEvents(OutcomeRefused, len(b.IDs))
		f["detail"] = res.Detail
		log.WithFields(f).Error("Activité Mail : lot refusé par le Hub, abandonné sans rejeu")
		return true
	}
	_ = MarkAttempt(ctx, db, b.IDs)
	st.failures++
	if st.firstFailure.IsZero() {
		st.firstFailure = now
	}
	f["failures"], f["detail"] = st.failures, res.Detail
	if res.Verdict == Unauthorized {
		st.deniedSecret = fingerprint(sub.Secret)
		st.pauseUntil = now.Add(Backoff(st.failures, 0, 15*time.Minute))
		log.WithFields(f).Error("Activité Mail : signature refusée par le Hub (401), rejeu après resynchronisation de l'abonnement")
		return false
	}
	st.pauseUntil = now.Add(Backoff(st.failures, res.RetryAfter, 30*time.Minute))
	f["retry_in_s"] = int(st.pauseUntil.Sub(now).Seconds())
	if now.Sub(st.firstFailure) >= w.StuckAfter {
		if now.Sub(st.lastLog) >= 10*time.Minute {
			st.lastLog = now
			f["since"] = st.firstFailure.UTC().Format(time.RFC3339)
			log.WithFields(f).Error("Activité Mail : le Hub ne prend plus les lots depuis plus d'une heure, la file grossit")
		}
	} else {
		log.WithFields(f).Warn("Activité Mail : lot non remis, rejeu avec attente")
	}
	return false
}

func fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "veridian_activite_outbox") && strings.Contains(err.Error(), "does not exist")
}
