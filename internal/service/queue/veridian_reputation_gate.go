package queue

import (
	"context"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (correctif 2026-09-29, mission "fusibles natifs de réputation") —
// circuit breaker de réputation PAR INFRA (domaine émetteur). Contrairement aux
// caps/warmup (veridian_daily_cap.go), qui bornent le VOLUME, ce fusible borne la
// QUALITÉ : il fige tout envoi depuis une infra dont le taux de bounce DUR sur 7
// jours glissants atteint 3%, ou dès qu'une PLAINTE (FBL) arrive — sans seuil,
// une seule suffit. C'est une vraie fonctionnalité native (comme les autres
// gates de ce fichier), pas un bricolage : même contrat skip-and-reschedule,
// même granularité par infra (domaine de l'adresse FROM), même source de vérité
// (message_history). Toujours actif, sans configuration : une infra qui abîme sa
// réputation n'attend pas qu'un opérateur pense à activer un garde-fou.
//
// Pourquoi une fenêtre de 7 jours plutôt qu'un flag persistant "frozen" : le taux
// se recalcule à CHAQUE gate-check depuis message_history (déjà la source de
// vérité de tout ce fichier) — pas de nouvelle colonne, pas de nouvelle migration,
// pas de risque de flag qui reste bloqué à true après correction. Le gel se lève
// naturellement quand la fenêtre glisse au-delà du lot fautif ET que les envois
// suivants sont propres ; un opérateur pressé peut aussi vider la fenêtre en
// changeant l'adresse d'envoi (nouveau domaine = nouveau compteur). La plainte,
// elle, gèle tant qu'elle reste dans la fenêtre de 7 jours : c'est le comportement
// volontairement le plus prudent (cf. mission : "si une plainte arrive").
//
// Signal visible : chaque gel logge le motif ("hard_bounce_rate" ou
// "complaint") avec les chiffres exacts (bounces/sent, ou nombre de plaintes) —
// même canal que veridianRescheduleCapped. Exposé aussi par API via
// GET /api/veridian/admin/reputation-status (veridian_reputation_handler.go),
// qui recalcule EXACTEMENT les mêmes requêtes pour ne jamais diverger du gate.

// veridianHardBounceRateFreezeThreshold est le seuil (proportion, pas %) de
// bounces durs sur la fenêtre au-delà duquel l'infra est gelée. 0.03 = 3%.
// Surchargeable par profil (EmailProvider.VeridianHardBounceFreezeThreshold, 2026-10-07).
const veridianHardBounceRateFreezeThreshold = domain.VeridianDefaultHardBounceFreezeThreshold

// veridianReputationWindow est la fenêtre glissante sur laquelle le taux de
// bounce dur et la présence d'une plainte sont évalués.
const veridianReputationWindow = 7 * 24 * time.Hour

// veridianReputationGate décide si l'infra émettrice de l'entrée doit être
// gelée pour cause de réputation dégradée. Retourne (délai, true) si l'envoi
// doit être reporté (SMTP jamais ouvert), (0, false) si l'infra est saine ou si
// aucune attribution d'infra n'est possible (best-effort, non-régression :
// une entrée sans FROM exploitable n'est pas plus bloquée qu'avant ce
// correctif). Une erreur DB fait échouer FERMÉ : cf. tête de fichier.
func (w *EmailQueueWorker) veridianReputationGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (time.Duration, bool) {
	senderDomain := veridianEmailDomain(entry.Payload.FromAddress)
	if senderDomain == "" {
		// Pas d'attribution infra possible : best-effort, on ne bloque pas
		// (identique au fallback des autres gates par infra de ce fichier).
		return 0, false
	}

	workspaceID := ""
	if workspace != nil {
		workspaceID = workspace.ID
	}
	since := time.Now().UTC().Add(-veridianReputationWindow)

	complaints, err := w.messageHistoryRepo.CountComplainedSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
	if err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":      entry.ID,
			"workspace_id":  workspaceID,
			"sender_domain": senderDomain,
			"error":         err.Error(),
		}).Error("Reputation fuse: complaint count failed; SMTP blocked (fail closed)")
		return veridianDailyCapRecheckInterval, true
	}
	if complaints > 0 {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"integration_id": entry.IntegrationID,
			"sender_domain":  senderDomain,
			"complaints_7d":  complaints,
			"fuse":           "complaint",
		}).Warn("Reputation fuse tripped: complaint recorded in the last 7 days, freezing infra")
		return veridianDailyCapRecheckInterval, true
	}

	sentCount, err := w.messageHistoryRepo.CountSentSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
	if err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":      entry.ID,
			"workspace_id":  workspaceID,
			"sender_domain": senderDomain,
			"error":         err.Error(),
		}).Error("Reputation fuse: sent count failed; SMTP blocked (fail closed)")
		return veridianDailyCapRecheckInterval, true
	}
	if sentCount == 0 {
		// Pas encore de volume sur la fenêtre : aucun taux calculable, rien à geler.
		return 0, false
	}

	hardBounces, err := w.messageHistoryRepo.CountHardBouncedSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
	if err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":      entry.ID,
			"workspace_id":  workspaceID,
			"sender_domain": senderDomain,
			"error":         err.Error(),
		}).Error("Reputation fuse: hard bounce count failed; SMTP blocked (fail closed)")
		return veridianDailyCapRecheckInterval, true
	}

	rate := float64(hardBounces) / float64(sentCount)
	threshold := provider.VeridianEffectiveHardBounceFreezeThreshold()
	if rate >= threshold {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":         entry.ID,
			"integration_id":   entry.IntegrationID,
			"sender_domain":    senderDomain,
			"hard_bounces_7d":  hardBounces,
			"sent_7d":          sentCount,
			"hard_bounce_rate": rate,
			"threshold":        threshold,
			"fuse":             "hard_bounce_rate",
		}).Warn("Reputation fuse tripped: hard bounce rate over threshold, freezing infra")
		return veridianDailyCapRecheckInterval, true
	}

	return 0, false
}

// VeridianReputationStatus is the read model shared by the gate above and the
// admin API (veridian_reputation_handler.go), so the two can never diverge:
// the handler calls VeridianComputeReputationStatus, which runs the exact same
// repository methods over the exact same window and applies the exact same
// threshold as the gate to decide Frozen.
type VeridianReputationStatus struct {
	SenderDomain    string  `json:"sender_domain"`
	WindowDays      int     `json:"window_days"`
	Sent7d          int     `json:"sent_7d"`
	HardBounces7d   int     `json:"hard_bounces_7d"`
	HardBounceRate  float64 `json:"hard_bounce_rate"`
	Threshold       float64 `json:"hard_bounce_rate_threshold"`
	ThresholdCustom bool    `json:"hard_bounce_rate_threshold_custom"`
	Complaints7d    int     `json:"complaints_7d"`
	Frozen          bool    `json:"frozen"`
	FrozenReason    string  `json:"frozen_reason,omitempty"`
}

// VeridianComputeReputationStatus computes the live reputation status for one
// sender domain — exported so the HTTP admin handler (and any future console
// panel) can read it without duplicating the gate's thresholds or queries.
func VeridianComputeReputationStatus(
	ctx context.Context,
	repo domain.MessageHistoryRepository,
	workspaceID, senderDomain string,
	provider *domain.EmailProvider,
	now time.Time,
) (VeridianReputationStatus, error) {
	status := VeridianReputationStatus{
		SenderDomain:    senderDomain,
		WindowDays:      int(veridianReputationWindow / (24 * time.Hour)),
		Threshold:       provider.VeridianEffectiveHardBounceFreezeThreshold(),
		ThresholdCustom: provider.VeridianHasCustomHardBounceFreezeThreshold(),
	}
	since := now.UTC().Add(-veridianReputationWindow)

	complaints, err := repo.CountComplainedSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	status.Complaints7d = complaints

	sentCount, err := repo.CountSentSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	status.Sent7d = sentCount

	hardBounces, err := repo.CountHardBouncedSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	status.HardBounces7d = hardBounces

	if sentCount > 0 {
		status.HardBounceRate = float64(hardBounces) / float64(sentCount)
	}

	switch {
	case complaints > 0:
		status.Frozen = true
		status.FrozenReason = "complaint"
	case status.HardBounceRate >= status.Threshold:
		status.Frozen = true
		status.FrozenReason = "hard_bounce_rate"
	}

	return status, nil
}

// VeridianEmailDomain exports veridianEmailDomain for callers outside this
// package (the reputation status service) that need the exact same domain
// extraction as the gate, so "which infra does this integration belong to"
// never diverges between the enforcement path and the read-only status API.
func VeridianEmailDomain(email string) string {
	return veridianEmailDomain(email)
}
