package queue

import (
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

// Veridian fork (fiche 62, lot 1 « pourquoi ça n'envoie pas », 10/10/2026).
//
// Persiste, pour chaque report, la RAISON dans l'entrée de file (même UPDATE que le
// report : coût ajouté nul) et, selon le niveau du workspace, une ligne du journal
// des décisions. Niveaux (domain.VeridianDecisionLog*) :
//   - off          : la raison reste sur l'entrée, aucune ligne de journal ;
//   - transitions  : DÉFAUT. Un report n'est journalisé que s'il est le premier de
//     l'entrée, si sa raison ou son profil change, au plus un battement par entrée et
//     par 24 h, et 1 examen sur 200 en trace complète (« sampled ») pour prouver que la
//     porte continue de tourner. Sans cela un ré-examen toutes les 5 min de 1 200
//     entrées écrirait ~345 000 lignes par jour ;
//   - all          : chaque examen (borné par la rétention du dépôt).
// Les envois, échecs et rejets par garde sont TOUJOURS journalisés (hors `off`).
// Tout est best-effort : une erreur d'écriture est loguée, elle ne bloque jamais un
// envoi ni un report.

const (
	veridianDecisionHeartbeat   = 24 * time.Hour
	veridianDecisionSampleEvery = 200
	veridianDecisionDetailMax   = 160
)

// SetDecisionLog branche le journal des décisions (nil = pas de journal).
func (w *EmailQueueWorker) SetDecisionLog(repo domain.VeridianSendDecisionRepository) {
	w.decisionLog = repo
}

func (w *EmailQueueWorker) veridianDecisionLevel(workspace *domain.Workspace) string {
	if workspace == nil {
		return domain.VeridianDecisionLogTransitions
	}
	return domain.VeridianNormalizeDecisionLogLevel(workspace.Settings.VeridianDecisionLogLevel)
}

// veridianDeferral décrit un report à persister.
type veridianDeferral struct {
	Reason        string
	Detail        string
	Profile       string
	Delay         time.Duration
	RefundAttempt bool
	LastError     string
}

func veridianTrimDetail(s string) string {
	if len(s) > veridianDecisionDetailMax {
		r := []rune(s)
		for len(string(r)) > veridianDecisionDetailMax {
			r = r[:len(r)-1]
		}
		return string(r)
	}
	return s
}

// veridianFailureReason classe un echec d'envoi en code de raison stable.
func veridianFailureReason(err error) string {
	if err == nil {
		return domain.VeridianReasonSendError
	}
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "excluded_provider_class"):
		return domain.VeridianReasonExcludedClass
	case strings.HasPrefix(msg, "pre-filtered recipient"):
		return "invalid_recipient"
	case strings.HasPrefix(msg, "render_at_send"):
		return domain.VeridianReasonRenderFailed
	}
	return domain.VeridianReasonSendError
}

// veridianShouldLogDeferral applique la politique d'échantillonnage du niveau
// « transitions ». Rend (écrire, échantillon, trace réduite).
func veridianShouldLogDeferral(level string, entry *domain.EmailQueueEntry, d veridianDeferral, now time.Time) (write, sampled, reduced bool) {
	switch level {
	case domain.VeridianDecisionLogOff:
		return false, false, false
	case domain.VeridianDecisionLogAll:
		return true, false, false
	}
	first := entry.DeferReason == ""
	changed := entry.DeferReason != d.Reason || entry.DeferProfile != d.Profile
	if first || changed {
		return true, false, false
	}
	if (entry.DeferCount+1)%veridianDecisionSampleEvery == 0 {
		return true, true, false
	}
	if entry.DecisionLoggedAt == nil || now.Sub(*entry.DecisionLoggedAt) >= veridianDecisionHeartbeat {
		return true, false, true
	}
	return false, false, false
}

// veridianDeferEntry persiste un report AVEC sa raison puis journalise la décision.
// legacy est l'ancien appel de report (SetNextRetry...) : il sert de repli si le
// dépôt ne sait pas porter la raison ou si l'écriture échoue (l'entrée doit TOUJOURS
// être re-planifiée, sinon elle serait ré-examinée en boucle).
func (w *EmailQueueWorker) veridianDeferEntry(
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
	d veridianDeferral,
	sel *veridianSelectionResult,
	legacy func() error,
) {
	now := time.Now().UTC()
	until := now.Add(d.Delay)
	level := w.veridianDecisionLevel(workspace)
	write, sampled, reduced := veridianShouldLogDeferral(level, entry, d, now)
	write = write && w.decisionLog != nil

	persisted := false
	if repo, ok := w.queueRepo.(domain.EmailQueueDeferralRepository); ok {
		err := repo.SetDeferral(w.ctx, workspace.ID, entry.ID, domain.EmailQueueDeferral{
			Reason:        d.Reason,
			Detail:        veridianTrimDetail(d.Detail),
			Profile:       d.Profile,
			Until:         until,
			RefundAttempt: d.RefundAttempt,
			LastError:     veridianTrimDetail(d.LastError),
			Logged:        write,
		})
		if err == nil {
			persisted = true
		} else {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"reason":   d.Reason,
				"error":    err.Error(),
			}).Warn("Failed to persist deferral reason, falling back to a plain reschedule")
		}
	}
	if !persisted && legacy != nil {
		if err := legacy(); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"reason":   d.Reason,
				"error":    err.Error(),
			}).Warn("Failed to reschedule deferred entry")
		}
	}

	if !write {
		return
	}
	untilCopy := until
	trace := w.veridianBuildTrace(workspace, entry, sel, domain.VeridianTraceDecision{
		Outcome: domain.VeridianOutcomeDeferred,
		Reason:  d.Reason,
		Detail:  d.Detail,
		Until:   &untilCopy,
		DelayS:  int(d.Delay / time.Second),
	}, reduced)
	w.veridianInsertDecision(workspace, entry, domain.VeridianOutcomeDeferred, d.Reason, d.Detail, &untilCopy, d.Profile, sampled, trace)
}

// veridianRecordTerminal journalise un envoi, un échec définitif ou un rejet par
// garde (toujours écrit, sauf niveau « off »).
func (w *EmailQueueWorker) veridianRecordTerminal(
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
	outcome, reason, detail, profile string,
	sel *veridianSelectionResult,
) {
	if w.decisionLog == nil || w.veridianDecisionLevel(workspace) == domain.VeridianDecisionLogOff {
		return
	}
	trace := w.veridianBuildTrace(workspace, entry, sel, domain.VeridianTraceDecision{
		Outcome: outcome, Reason: reason, Detail: veridianTrimDetail(detail),
	}, false)
	w.veridianInsertDecision(workspace, entry, outcome, reason, veridianTrimDetail(detail), nil, profile, false, trace)
}

func (w *EmailQueueWorker) veridianInsertDecision(
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
	outcome, reason, detail string,
	until *time.Time,
	profile string,
	sampled bool,
	trace *domain.VeridianSendTrace,
) {
	if profile == "" {
		profile = entry.IntegrationID
	}
	dec := &domain.VeridianSendDecision{
		ID:           uuid.NewString(),
		At:           time.Now().UTC(),
		EntryID:      entry.ID,
		MessageID:    entry.MessageID,
		ContactEmail: entry.ContactEmail,
		NodeID:       entry.NodeID,
		Outcome:      outcome,
		Reason:       reason,
		Detail:       detail,
		Until:        until,
		ProfileID:    profile,
		Sampled:      sampled,
		Trace:        trace,
	}
	if entry.SourceType == domain.EmailQueueSourceAutomation {
		dec.AutomationID = entry.SourceID
	}
	if err := w.decisionLog.Insert(w.ctx, workspace.ID, dec); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"outcome":  outcome,
			"error":    err.Error(),
		}).Warn("Failed to write send decision (best effort)")
	}
}

// veridianBuildTrace assemble la trace gate par gate d'une décision. sel peut être
// nil (échec, rejet par garde : pas de sélection). reduced garde seulement les portes
// bloquantes (battements).
func (w *EmailQueueWorker) veridianBuildTrace(
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
	sel *veridianSelectionResult,
	decision domain.VeridianTraceDecision,
	reduced bool,
) *domain.VeridianSendTrace {
	trace := &domain.VeridianSendTrace{
		Level:      "full",
		Candidates: []domain.VeridianCandidateTrace{},
		Decision:   decision,
	}
	if reduced {
		trace.Level = "reduced"
	}
	if sel == nil {
		trace.Class = entry.Payload.VeridianProviderClass
		return trace
	}
	trace.Class = sel.Class
	if sel.Anchor.Found {
		trace.Anchor = &domain.VeridianTraceAnchor{Profile: sel.Anchor.ID, Available: sel.Anchor.Available}
	}
	for _, ev := range sel.Evaluations {
		name := ""
		if integ := workspace.GetIntegrationByID(ev.Candidate.IntegrationID); integ != nil {
			name = integ.Name
		}
		ct := ev.trace(name)
		if reduced {
			kept := ct.Gates[:0:0]
			for _, g := range ct.Gates {
				if g.Verdict == domain.VeridianVerdictBlock {
					kept = append(kept, g)
				}
			}
			ct.Gates = kept
		}
		if ct.Gates == nil {
			ct.Gates = []domain.VeridianGateRecord{}
		}
		trace.Candidates = append(trace.Candidates, ct)
	}
	return trace
}
