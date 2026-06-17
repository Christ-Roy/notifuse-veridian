package service

import (
	"context"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork — émission des events COMPORTEMENTAUX vers le Hub (open/click,
// 2026-06-17). Le Hub a livré en prod un réconciliateur de scoring prospect
// (ingestProspectEvent) qui attend email.opened / email.clicked / email.replied.
// Notifuse possédait la donnée (SetOpened/SetClicked) mais ne l'émettait jamais.
//
// On RÉUTILISE le VeridianWebhookEmitter existant (voie legacy HMAC consommée par
// le Hub) — PAS de nouveau canal. L'emitter est injecté en DI optionnelle
// (SetVeridianWebhookEmitter) après sa création dans app.go (il est instancié
// bien après EmailService). emitter nil = no-op strict (non-régression : Notifuse
// self-hosted ou Hub non configuré → aucun event, comportement upstream inchangé).
//
// BEST-EFFORT de bout en bout : Emit lui-même part dans une goroutine (retry 3x),
// donc ne bloque jamais le hot path (pixel d'ouverture, redirection de clic). La
// résolution du contact_email (FindContactEmailByMessageID) est un lookup index-only
// par id ; un échec/miss dégrade en « pas d'émission » et ne fait jamais échouer le
// tracking (l'open/click est déjà persisté côté Notifuse de toute façon).
//
// L'appelant (handler email_handler.go) gère DÉJÀ l'anti-bot (IsBotUserAgent,
// open/click < 7s ignoré) : OpenEmail/VisitLink ne sont appelés QUE quand
// shouldRecord == true. On n'émet donc que des engagements réels — pas de pollution
// du score par des opens proxy/bot.

// SetVeridianWebhookEmitter injecte (post-construction) l'emitter Hub utilisé pour
// pousser les events comportementaux. Optionnel et nil-safe : sans emitter, l'envoi
// d'events est désactivé silencieusement.
func (s *EmailService) SetVeridianWebhookEmitter(emitter domain.WebhookEmitter) {
	s.veridianWebhookEmitter = emitter
}

// veridianEmitBehavioral résout le contact_email de l'envoi puis pousse l'event
// comportemental vers le Hub. No-op si l'emitter n'est pas configuré. Best-effort :
// jamais d'erreur retournée, jamais de blocage (Emit est async).
//
// extra permet d'ajouter des champs spécifiques (ex. link_url pour email.clicked).
func (s *EmailService) veridianEmitBehavioral(
	ctx context.Context,
	eventType domain.VeridianEvent,
	workspaceID, messageID string,
	extra map[string]interface{},
) {
	if s.veridianWebhookEmitter == nil {
		return
	}

	// contact_email = clé de jointure V1 du scoring Hub. Sans elle, l'event est
	// ingéré pour forensics mais ne déplace aucun score → on émet quand même (le Hub
	// le tolère), mais on log le miss pour diagnostic.
	contactEmail, found, err := s.messageRepo.FindContactEmailByMessageID(ctx, workspaceID, messageID)
	if err != nil {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"event_type":   string(eventType),
				"workspace_id": workspaceID,
				"message_id":   messageID,
				"error":        err.Error(),
			}).Warn("veridian behavioral: contact_email lookup failed, emitting without join key")
		}
	} else if !found && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"event_type":   string(eventType),
			"workspace_id": workspaceID,
			"message_id":   messageID,
		}).Debug("veridian behavioral: message not found for contact_email lookup")
	}

	data := map[string]interface{}{
		"message_id":  messageID,
		"occurred_at": time.Now().UTC().Format(time.RFC3339),
	}
	if contactEmail != "" {
		data["contact_email"] = domain.VeridianNormalizeEmail(contactEmail)
	}
	for k, v := range extra {
		data[k] = v
	}

	s.veridianWebhookEmitter.Emit(ctx, eventType, workspaceID, data)
}
