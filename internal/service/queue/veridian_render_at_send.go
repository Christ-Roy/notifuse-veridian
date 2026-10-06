package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/emailerror"
)

// Veridian fork — RENDU AU DEPILAGE (render-at-send).
//
// Le defaut corrige : les noeuds email des automations stockaient dans
// email_queue.payload le sujet, le texte et le html DEJA rendus a l'inscription.
// Corriger un modele ne touchait donc pas les messages deja en file : ils
// partaient avec l'ANCIEN texte (25 942 messages en file sur robertbrunon le
// 06/10/2026 quand le defaut a ete releve).
//
// Maintenant, pour toute entree d'automation portant un template_id, le worker
// re-rend sujet / texte / html / plain_text_only / reply-to JUSTE AVANT l'envoi,
// a partir de la version COURANTE du modele et des donnees COURANTES du contact
// (cf. service.VeridianQueueEmailRenderer, qui partage le moteur de rendu avec
// l'enqueue). Le payload n'est plus que le repli de ce qui n'a pas de modele.
//
// Garanties :
//   - rendu en echec (modele supprime, contact supprime, MJML/Liquid invalide,
//     sujet ou corps vide) : le message NE PART PAS, il est marque en echec avec
//     une raison lisible dans message_history.status_info ("render_at_send: ...")
//     et le contact d'automation est resolu par le callback d'echec habituel ;
//   - panne transitoire (lecture DB) : retry avec le backoff habituel, sans
//     jamais envoyer le contenu fige ;
//   - le From, l'integration et la rotation de pool restent ceux decides par le
//     failover (le rendu ne touche pas a l'identite d'envoi) ;
//   - idempotence / dedup / plafonds / anti-hash : inchanges (ce gate ne lit ni
//     n'ecrit aucun compteur).
//
// Sans renderer cable (constructeur upstream utilise tel quel par les tests),
// le comportement historique est strictement conserve.

// QueuedEmailRenderer re-rend le contenu d'une entree de file a partir du modele
// et du contact courants. Implemente par service.VeridianQueueEmailRenderer
// (interface ici : le package service importe queue, pas l'inverse).
type QueuedEmailRenderer interface {
	RenderQueuedEmail(ctx context.Context, workspace *domain.Workspace, entry *domain.EmailQueueEntry) (*RenderedQueuedEmail, error)
}

// RenderedQueuedEmail est le contenu a envoyer, issu du modele courant.
type RenderedQueuedEmail struct {
	Subject         string
	HTMLContent     string
	TextContent     string
	PlainTextOnly   bool
	ReplyTo         string
	TemplateVersion int
}

// RenderError qualifie un echec de rendu. Permanent = inutile de re-essayer
// (modele ou contact supprime, modele invalide, contenu vide) ; sinon transitoire.
type RenderError struct {
	Reason    string
	Permanent bool
	Err       error
}

func (e *RenderError) Error() string {
	if e.Err == nil {
		return "render_at_send: " + e.Reason
	}
	return fmt.Sprintf("render_at_send: %s: %v", e.Reason, e.Err)
}

func (e *RenderError) Unwrap() error { return e.Err }

// SetQueuedEmailRenderer branche le rendu au depilage. Setter (et non argument
// du constructeur) pour conserver la surface du constructeur upstream.
func (w *EmailQueueWorker) SetQueuedEmailRenderer(r QueuedEmailRenderer) {
	w.queuedEmailRenderer = r
}

// veridianValidateRendered refuse tout contenu vide : on n'envoie jamais un mail
// sans sujet ou sans corps.
func veridianValidateRendered(out *RenderedQueuedEmail) error {
	if out == nil {
		return &RenderError{Reason: "renderer returned no content", Permanent: true}
	}
	if strings.TrimSpace(out.Subject) == "" {
		return &RenderError{Reason: "le sujet rendu est vide", Permanent: true}
	}
	if out.PlainTextOnly {
		if strings.TrimSpace(out.TextContent) == "" {
			return &RenderError{Reason: "mail texte seul : le texte rendu est vide", Permanent: true}
		}
		return nil
	}
	if strings.TrimSpace(out.HTMLContent) == "" && strings.TrimSpace(out.TextContent) == "" {
		return &RenderError{Reason: "le corps rendu (html et texte) est vide", Permanent: true}
	}
	return nil
}

// veridianRenderAtSend remplace le contenu fige de l'entree par le rendu courant.
// Retourne true si l'envoi peut continuer ; false si l'entree a ete traitee
// (echec marque ou retard programme) ou si le worker s'arrete.
func (w *EmailQueueWorker) veridianRenderAtSend(workspace *domain.Workspace, entry *domain.EmailQueueEntry) bool {
	if w.queuedEmailRenderer == nil ||
		entry.SourceType != domain.EmailQueueSourceAutomation ||
		entry.TemplateID == "" {
		return true
	}

	out, err := w.queuedEmailRenderer.RenderQueuedEmail(w.ctx, workspace, entry)
	if err == nil {
		err = veridianValidateRendered(out)
	}
	if err == nil {
		changed := out.Subject != entry.Payload.Subject ||
			out.HTMLContent != entry.Payload.HTMLContent ||
			out.TextContent != entry.Payload.TextContent
		entry.Payload.Subject = out.Subject
		entry.Payload.HTMLContent = out.HTMLContent
		entry.Payload.TextContent = out.TextContent
		entry.Payload.PlainTextOnly = out.PlainTextOnly
		entry.Payload.EmailOptions.ReplyTo = out.ReplyTo
		entry.Payload.TemplateVersion = out.TemplateVersion
		if changed {
			w.logger.WithFields(map[string]interface{}{
				"entry_id":         entry.ID,
				"message_id":       entry.MessageID,
				"template_id":      entry.TemplateID,
				"template_version": out.TemplateVersion,
			}).Info("Render-at-send: content differs from the one frozen at enqueue, sending the current template")
		}
		return true
	}

	if w.ctx.Err() != nil {
		return false // arret du worker : rien a marquer
	}

	// Echec : le message ne part pas. MarkAsProcessing d'abord (incremente
	// attempts) car handleError suppose le compteur avance.
	if mErr := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); mErr != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"error":    mErr.Error(),
		}).Warn("Failed to mark entry as processing for render-at-send failure")
		return false
	}

	var renderErr *RenderError
	if !errors.As(err, &renderErr) {
		renderErr = &RenderError{Reason: "echec de rendu", Err: err}
	}
	w.logger.WithFields(map[string]interface{}{
		"entry_id":    entry.ID,
		"message_id":  entry.MessageID,
		"template_id": entry.TemplateID,
		"recipient":   entry.ContactEmail,
		"permanent":   renderErr.Permanent,
		"error":       renderErr.Error(),
	}).Warn("Render-at-send failed, message not sent")

	if renderErr.Permanent {
		w.handleError(workspace, entry, renderErr, &emailerror.ClassifiedError{
			Original:  renderErr,
			Type:      emailerror.ErrorTypeRecipient, // ne compte jamais pour le circuit breaker
			Retryable: false,
		})
	} else {
		w.handleError(workspace, entry, renderErr, nil) // retry avec backoff
	}
	return false
}
