package queue

import (
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — FILET best-effort de l'anti-hash identique au worker (cold
// outbound). La GARANTIE de variété est posée à l'ENQUEUE (le sender détecte la
// collision et re-spinne, cf. veridian_content_dedup.go) : c'est là qu'on a le
// template brut pour re-varier. Ce gate-ci est le filet de SÉCURITÉ ultime : il
// CONSTATE une collision résiduelle (un hash déjà envoyé vers la même classe
// dans la fenêtre est arrivé jusqu'au worker malgré l'enqueue) et la TRACE, mais
// ne BLOQUE PAS l'envoi.
//
// Pourquoi log-only et pas un skip dur : le worker n'a pas le template brut (il
// consomme un payload figé), il ne peut donc PAS re-varier. Bloquer ici ne ferait
// que geler un mail que l'enqueue a délibérément laissé passer (template sans
// spintax = aucune variante possible → on assume l'envoi plutôt que de ne jamais
// contacter le prospect, cf. ticket "pas de perte de mail"). Le worker observe et
// alerte ; le linter (amont) et le re-spin (enqueue) font le vrai travail.
//
// Best-effort STRICT : pas de hash sur le payload (envoi non-cold / anti-hash
// off) = no-op ; erreur DB sur l'EXISTS = log + pass (jamais de blocage sur un
// incident de lecture). Placé dans processEntry APRÈS le pré-filtre, AVANT
// MarkAsProcessing (constat sans effet de bord sur les attempts).

// veridianContentHashGate observe une éventuelle collision de hash de contenu
// vers la même classe de provider destinataire et la logge (best-effort). Ne
// retourne RIEN d'actionnable : l'envoi continue toujours. provider est l'infra
// d'envoi (cascade de config anti-hash). No-op si le payload ne porte pas de
// hash (hors contexte cold / anti-hash désactivé).
func (w *EmailQueueWorker) veridianContentHashGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) {
	hash := entry.Payload.VeridianContentHash
	if hash == "" {
		return // pas de hash = anti-hash inactif sur cet envoi.
	}
	if w.messageHistoryRepo == nil {
		return
	}

	// Résolution de la fenêtre via la cascade infra → workspace (le broadcast a
	// déjà été collapsé dans le payload à l'enqueue ; ici on n'a que le hash, pas
	// la window broadcast — on retombe sur infra puis workspace puis défaut).
	window := veridianResolveAntiHashWindow(workspace, provider)

	workspaceID := ""
	if workspace != nil {
		workspaceID = workspace.ID
	}
	since := time.Now().Add(-window)

	class := w.veridianClassifyRecipient(entry)
	domains, exclude := domain.VeridianDomainsForClass(class)

	exists, err := w.messageHistoryRepo.ExistsContentHashSince(w.ctx, workspaceID, hash, domains, exclude, since)
	if err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":     entry.ID,
			"workspace_id": workspaceID,
			"error":        err.Error(),
		}).Warn("Anti-hash residual check failed, allowing send (best-effort)")
		return
	}
	if exists {
		// Collision résiduelle : l'enqueue n'a pas pu varier (template sans
		// spintax). On TRACE pour alerter l'utilisateur (variété insuffisante),
		// mais on ENVOIE quand même (pas de perte de mail).
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"integration_id": entry.IntegrationID,
			"provider_class": class,
			"recipient":      entry.ContactEmail,
			"content_hash":   hash,
			"window":         window.String(),
		}).Warn("Anti-hash residual collision: identical content already sent to this provider class in window (template lacks spintax variety); sending anyway")
	}
}

// veridianResolveAntiHashWindow résout la fenêtre glissante anti-hash au worker
// via la cascade infra (EmailProvider) → workspace → défaut. Le niveau broadcast
// n'est pas disponible ici (collapsé dans le payload à l'enqueue, sans la
// window) ; le worker n'étant qu'un filet, retomber sur infra/workspace/défaut
// est suffisant. Premier niveau strictement positif gagne.
func veridianResolveAntiHashWindow(workspace *domain.Workspace, provider *domain.EmailProvider) time.Duration {
	infraHours := 0
	if provider != nil {
		infraHours = provider.VeridianAntiHashWindowHours
	}
	workspaceHours := 0
	if workspace != nil {
		workspaceHours = workspace.Settings.VeridianAntiHashWindowHours
	}
	return domain.VeridianAntiHashWindow(0, infraHours, workspaceHours)
}
