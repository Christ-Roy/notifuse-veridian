package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
)

// VeridianQueueEmailRenderer implemente queue.QueuedEmailRenderer : il re-rend
// au depilage le contenu d'une entree d'automation avec la version COURANTE du
// modele et les donnees COURANTES du contact, via le meme moteur que l'enqueue
// (renderAutomationEmail). Cf. queue/veridian_render_at_send.go.
type VeridianQueueEmailRenderer struct {
	templateRepo   domain.TemplateRepository
	listRepo       domain.ListRepository
	contactRepo    domain.ContactRepository
	automationRepo domain.AutomationRepository
	apiEndpoint    string
}

// NewVeridianQueueEmailRenderer cree le renderer de depilage.
func NewVeridianQueueEmailRenderer(
	templateRepo domain.TemplateRepository,
	listRepo domain.ListRepository,
	contactRepo domain.ContactRepository,
	automationRepo domain.AutomationRepository,
	apiEndpoint string,
) *VeridianQueueEmailRenderer {
	return &VeridianQueueEmailRenderer{
		templateRepo:   templateRepo,
		listRepo:       listRepo,
		contactRepo:    contactRepo,
		automationRepo: automationRepo,
		apiEndpoint:    apiEndpoint,
	}
}

// RenderQueuedEmail rend l'entree. Toute erreur est un *queue.RenderError,
// Permanent quand reessayer ne changera rien.
func (r *VeridianQueueEmailRenderer) RenderQueuedEmail(
	ctx context.Context,
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
) (*queue.RenderedQueuedEmail, error) {
	automation, err := r.automationRepo.GetByID(ctx, workspace.ID, entry.SourceID)
	if err != nil {
		var notFound *domain.ErrAutomationNotFound
		return nil, &queue.RenderError{
			Reason:    fmt.Sprintf("automation %s illisible", entry.SourceID),
			Permanent: errors.As(err, &notFound),
			Err:       err,
		}
	}

	template, err := r.templateRepo.GetTemplateByID(ctx, workspace.ID, entry.TemplateID, 0)
	if err != nil {
		var notFound *domain.ErrTemplateNotFound
		return nil, &queue.RenderError{
			Reason:    fmt.Sprintf("modele %s illisible", entry.TemplateID),
			Permanent: errors.As(err, &notFound),
			Err:       err,
		}
	}

	contact, err := r.contactRepo.GetContactByEmail(ctx, workspace.ID, entry.ContactEmail)
	if err != nil {
		return nil, &queue.RenderError{
			Reason:    "contact illisible",
			Permanent: errors.Is(err, domain.ErrContactNotFound),
			Err:       err,
		}
	}

	listID := automation.ListID
	if listID == "" {
		listID = entry.Payload.ListID
	}
	listName := ""
	if listID != "" {
		list, lErr := r.listRepo.GetListByID(ctx, workspace.ID, listID)
		if lErr != nil {
			return nil, &queue.RenderError{
				Reason: fmt.Sprintf("liste %s illisible", listID),
				Err:    lErr,
			}
		}
		listID = list.ID
		listName = list.Name
	}

	rendered, err := renderAutomationEmail(automationEmailRenderInput{
		Template:       template,
		Workspace:      workspace,
		APIEndpoint:    r.apiEndpoint,
		AutomationID:   automation.ID,
		AutomationName: automation.Name,
		ListID:         listID,
		ListName:       listName,
		TemplateID:     entry.TemplateID,
		MessageID:      entry.MessageID,
		Contact:        contact,
	})
	if err != nil {
		return nil, &queue.RenderError{Reason: "modele non rendable", Permanent: true, Err: err}
	}

	return &queue.RenderedQueuedEmail{
		Subject:         rendered.Subject,
		HTMLContent:     rendered.HTML,
		TextContent:     rendered.Text,
		PlainTextOnly:   rendered.PlainTextOnly,
		ReplyTo:         rendered.ReplyTo,
		TemplateVersion: int(template.Version),
	}, nil
}
