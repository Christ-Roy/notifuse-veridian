package queue

import (
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — gate de PLAFOND JOURNALIER par ADRESSE ÉMETTRICE (warmup IP, cold
// outbound). Quatrième dimension de plafonnement, JUMELLE de veridianDailyCapGate
// mais keyée ÉMETTEUR au lieu de DESTINATAIRE. Appelée par worker.go:processEntry
// JUSTE APRÈS le daily cap destinataire/classe (les deux raisonnent sur le même
// jour calendaire UTC et la même source message_history), avant la fenêtre
// d'envoi. Même contrat skip-and-reschedule que les autres gates : une entrée
// plafonnée est re-planifiée via SetNextRetry SANS incrémenter les attempts (pas
// de consommation de retry, pas de head-of-line blocking).
//
// Pourquoi une dimension émettrice DISTINCTE des caps destinataire/classe : le
// daily cap existant protège la RÉPUTATION CÔTÉ RECEVEUR (max N/jour vers une
// adresse / une classe Gmail-Outlook-…). Le cap par sender protège la MONTÉE EN
// CHARGE DE NOS PROPRES BOÎTES (warmup IP/domaine) : une IP/un domaine frais ne
// doit pas envoyer 200 mails dès J1, indépendamment de À QUI. C'est le warmup IP
// classique (Lemlist/Instantly), où chaque boîte monte son propre volume jour
// après jour. Les deux dimensions coexistent : le PLUS RESTRICTIF gagne (chacune
// peut déclencher le skip ; on teste émetteur après destinataire/classe).
//
// Source de vérité = message_history filtré par l'adresse FROM (colonne
// veridian_sender_email, V53), COUNT depuis minuit UTC : pas de table compteur,
// pas de cron de reset, survit aux redémarrages worker (comme le daily cap).
//
// Résolution de la config (du plus spécifique au plus général), identique aux
// caps : payload (copié à l'enqueue depuis broadcast.metadata) → infra
// (EmailProvider, warmup par IP) → workspace settings (lus en live) → rien =
// no-op strict (non-régression upstream).
//
// Best-effort : une erreur DB sur le COUNT NE bloque PAS l'envoi (on dégrade vers
// "pas de cap" et on log). Le throttle minute et les caps destinataire restent
// appliqués par-dessus.

// veridianResolvePerSenderCap résout le plafond émetteur de la cascade
// (broadcast → infra → workspace). Premier niveau > 0 gagne. provider peut être
// nil (legacy) → niveau infra sauté. 0 = pas de plafond émetteur.
func veridianResolvePerSenderCap(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) int {
	switch {
	case entry.Payload.VeridianPerSenderDailyCap > 0:
		return entry.Payload.VeridianPerSenderDailyCap
	case provider != nil && provider.VeridianPerSenderDailyCap > 0:
		return provider.VeridianPerSenderDailyCap
	case workspace != nil && workspace.Settings.VeridianPerSenderDailyCap > 0:
		return workspace.Settings.VeridianPerSenderDailyCap
	}
	return 0
}

// veridianPerSenderCapGate décide si l'entrée doit être reportée pour cause de
// plafond journalier ÉMETTEUR. Retourne (délai, true) si l'entrée doit être
// re-planifiée, (0, false) si elle peut partir. No-op strict sans configuration
// OU sans adresse FROM exploitable (entry.Payload.FromAddress vide).
func (w *EmailQueueWorker) veridianPerSenderCapGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (time.Duration, bool) {
	cap := veridianResolvePerSenderCap(workspace, provider, entry)
	if cap <= 0 {
		return 0, false
	}

	// Sans adresse FROM connue, le COUNT par sender n'a pas de clé : on ne peut pas
	// attribuer l'envoi à une boîte → on n'enforce pas (best-effort, non-régression).
	sender := entry.Payload.FromAddress
	if sender == "" {
		return 0, false
	}

	workspaceID := ""
	if workspace != nil {
		workspaceID = workspace.ID
	}
	since := veridianStartOfDayUTC(time.Now())

	count, err := w.messageHistoryRepo.CountSentSinceForSender(w.ctx, workspaceID, sender, since)
	if err != nil {
		// Best-effort : une erreur de COUNT ne bloque jamais l'envoi.
		w.logger.WithFields(map[string]interface{}{
			"entry_id":     entry.ID,
			"workspace_id": workspaceID,
			"sender":       sender,
			"error":        err.Error(),
		}).Warn("Daily per-sender cap count failed, allowing send (degraded)")
		return 0, false
	}
	if count >= cap {
		// Réutilise le reschedule du daily cap (même délai borné, même contrat).
		// capKind "per_sender" pour la traçabilité du motif dans les logs.
		return w.veridianRescheduleCapped(entry, "per_sender", count, cap, "")
	}

	return 0, false
}
