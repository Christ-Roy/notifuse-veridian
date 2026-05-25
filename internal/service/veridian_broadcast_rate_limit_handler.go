package service

// === Veridian patch — Broadcast rate-limit handler (Hub Mail Gateway) ===
//
// Wrapper transitoire qui intercepte les retours `recipient_rate_limited`
// (Hub Mail Gateway v1.1 — POST /api/mail/send-as-user) avant qu'ils ne
// plantent un batch broadcast entier. Pattern : iterer les destinataires
// un par un, ne jamais propager d'erreur fatale, retourner un slice
// `BroadcastSendResult` que le caller broadcast utilise pour decider
// quoi logger / persister dans `message_history`.
//
// Cycle de vie : ce wrapper existe parce que le refactor `EmailService`
// global (cf. todo/2026-05-26-mail-gateway-refactor-emailservice.md
// §3.3) n'est pas livre — l'EmailService upstream route tout via SMTP
// generique et ignore le choix `workspace.MailProviderChoice`. Quand le
// refactor §3.3 sera livre, le skip recipient_rate_limited migrera
// directement dans EmailService et ce wrapper deviendra inutile (a
// supprimer dans le ticket de cleanup post-§3.3).
//
// Pour les sub-agents qui le wirent : ce handler n'est PAS branche par
// defaut. Les call sites broadcast doivent l'appeler explicitement quand
// le workspace cible utilise `workspace.MailProviderChoice == hub_gmail`
// (cf. ticket parent). Sinon ils continuent de router via le code SMTP
// upstream existant — pas de regression vague 6.
//
// Reference :
//   - pkg/hub_mail_gateway/client.go (lib v1.1 livree par mail-gateway-lib-v2)
//   - Spec Hub : ../veridian-hub/todo/2026-05-25-mail-provider-status-endpoint.md §4
//   - Ticket parent : todo/2026-05-26-mail-gateway-refactor-emailservice.md

import (
	"context"

	"github.com/Notifuse/notifuse/pkg/hub_mail_gateway"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

// BroadcastSendResult — verdict d'envoi pour UN destinataire dans un batch
// broadcast. Toujours peuple (jamais nil dans le slice retourne par
// SendToRecipients), meme en cas d'erreur — le caller iterera proprement
// pour journaliser chaque verdict dans message_history.
//
// Semantique des champs :
//   - Recipient : email cible (toujours peuple)
//   - OK : true si Hub a accepte + envoye (MessageID peuple)
//   - MessageID : id du mail envoye cote Hub (vide si OK == false)
//   - Reason : code court d'echec (vide si OK == true). Stable cross-version,
//     identique aux constantes ReasonXxx de pkg/hub_mail_gateway.
//   - RetryAfterSeconds : non-zero UNIQUEMENT quand Reason ==
//     hub_mail_gateway.ReasonRecipientRateLimited. Le caller peut planifier
//     un re-essai apres ce delai (ex: requeue le destinataire dans le batch
//     suivant avec ETA = now + RetryAfterSeconds).
type BroadcastSendResult struct {
	Recipient         string
	OK                bool
	MessageID         string
	Reason            string
	RetryAfterSeconds int
}

// BroadcastRateLimitHandler — interface du wrapper. Facilite le mock cote
// caller broadcasts (le caller broadcasts pourra etre teste avec un fake
// handler qui retourne directement le slice de verdicts).
type BroadcastRateLimitHandler interface {
	SendToRecipients(ctx context.Context, params hub_mail_gateway.SendMailParams, recipients []string) []BroadcastSendResult
}

// broadcastRateLimitHandler — implementation par defaut. Tape directement
// sur le hub_mail_gateway.Client lib v1.1.
type broadcastRateLimitHandler struct {
	mailGateway hub_mail_gateway.Client
	logger      logger.Logger
	// newIdempotencyKey est injectable pour permettre des tests deterministes
	// (le pattern "uuid.New().String()" rend les body assertions fragiles).
	// Par defaut : uuid.New().String().
	newIdempotencyKey func() string
}

// NewBroadcastRateLimitHandler — constructeur. mailGateway et logger
// peuvent etre nil pour tests legers (logger nil => warnings silencieux ;
// mailGateway nil => SendToRecipients retourne des results avec Reason
// "send_error" pour chaque destinataire).
func NewBroadcastRateLimitHandler(mailGateway hub_mail_gateway.Client, log logger.Logger) BroadcastRateLimitHandler {
	return &broadcastRateLimitHandler{
		mailGateway:       mailGateway,
		logger:            log,
		newIdempotencyKey: func() string { return uuid.New().String() },
	}
}

// SendToRecipients itere `recipients` UN PAR UN et delegue chaque envoi
// au Hub Mail Gateway. Garantie : pour chaque destinataire d'entree, un
// `BroadcastSendResult` est present dans le slice de sortie, dans le
// MEME ORDRE.
//
// Politique d'erreur :
//   - Hub retourne OK -> result.OK = true + MessageID
//   - Hub retourne Reason == ReasonRecipientRateLimited (429) -> SKIP,
//     log Warn rate-limite, on continue les autres destinataires.
//     RetryAfterSeconds est preserve.
//   - Hub retourne autre Reason (needs_reauth, provider_not_linked,
//     unreachable, etc.) -> on capture le Reason dans le result, on
//     continue les autres destinataires (no-fatal-fail-fast).
//   - mailGateway.SendMailAsUser retourne err non-nil (ErrInvalidParams,
//     marshal failure, etc.) -> result.Reason = "send_error", on continue.
//   - `params` invalide (subject vide, etc.) -> meme comportement, propage
//     par le client.SendMailAsUser qui retourne err.
//
// Le caller decide ensuite quoi faire des verdicts : persister dans
// message_history, requeue les rate-limited dans un batch suivant, etc.
//
// Note IdempotencyKey : `params.IdempotencyKey` est IGNORE — on en
// genere un par destinataire (sinon le Hub deduplique le 2eme, 3eme...
// destinataire comme replay du 1er). Si tu veux deduper un retry complet
// du batch, fournis idempotency_key cote message_history en amont et
// passe-le dans params via l'override `newIdempotencyKey` (test-only,
// pas exporte en prod).
func (h *broadcastRateLimitHandler) SendToRecipients(ctx context.Context, params hub_mail_gateway.SendMailParams, recipients []string) []BroadcastSendResult {
	results := make([]BroadcastSendResult, 0, len(recipients))

	for _, recipient := range recipients {
		// Copy params per recipient — anti-aliasing (To slice partage = bug)
		p := params
		p.To = []string{recipient}
		p.IdempotencyKey = h.newIdempotencyKey()

		// Si mailGateway est nil (cas tests / mode degrade), on retourne
		// directement un send_error et on continue.
		if h.mailGateway == nil {
			results = append(results, BroadcastSendResult{
				Recipient: recipient,
				OK:        false,
				Reason:    "send_error",
			})
			continue
		}

		result, err := h.mailGateway.SendMailAsUser(ctx, p)
		if err != nil {
			// Erreur de programmation (params invalides, marshal) ou disabled
			// (ErrMailGatewayDisabled). On capture send_error et on continue.
			if h.logger != nil {
				h.logger.WithFields(map[string]interface{}{
					"recipient": recipient,
					"error":     err.Error(),
				}).Warn("broadcast: send_error on recipient, skipped")
			}
			results = append(results, BroadcastSendResult{
				Recipient: recipient,
				OK:        false,
				Reason:    "send_error",
			})
			continue
		}

		// result garanti non-nil quand err == nil (contrat client v1.1).
		if result.OK {
			results = append(results, BroadcastSendResult{
				Recipient: recipient,
				OK:        true,
				MessageID: result.MessageID,
			})
			continue
		}

		// Echec previsible. Cas special recipient_rate_limited = SKIP visible
		// (log Warn + RetryAfterSeconds preserve).
		if result.Reason == hub_mail_gateway.ReasonRecipientRateLimited {
			if h.logger != nil {
				h.logger.WithFields(map[string]interface{}{
					"recipient":           recipient,
					"retry_after_seconds": result.RetryAfterSeconds,
				}).Warn("broadcast: recipient rate-limited, skipped")
			}
			results = append(results, BroadcastSendResult{
				Recipient:         recipient,
				OK:                false,
				Reason:            hub_mail_gateway.ReasonRecipientRateLimited,
				RetryAfterSeconds: result.RetryAfterSeconds,
			})
			continue
		}

		// Autre echec previsible (needs_reauth, provider_not_linked,
		// unreachable, ...). On capture le Reason brut et on continue le
		// batch — la decision de retry / banner / disable provider est
		// laissee au caller broadcast qui agrege les verdicts.
		if h.logger != nil {
			h.logger.WithFields(map[string]interface{}{
				"recipient":   recipient,
				"reason":      result.Reason,
				"http_status": result.HTTPStatus,
			}).Warn("broadcast: send failed on recipient, skipped")
		}
		results = append(results, BroadcastSendResult{
			Recipient: recipient,
			OK:        false,
			Reason:    result.Reason,
		})
	}

	return results
}
