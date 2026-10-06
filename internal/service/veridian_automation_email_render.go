package service

import (
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/Notifuse/notifuse/pkg/veridian_spintax"
)

// Veridian fork — rendu d'un email d'automation, partage entre l'enqueue
// (EmailNodeExecutor) et le depilage (VeridianQueueEmailRenderer).
//
// Pourquoi une fonction unique : le rendu ne doit exister qu'a UN seul endroit.
// Avant, le noeud email rendait sujet/texte/html a l'inscription et la file
// stockait le resultat fige : corriger un modele ne touchait pas les messages
// deja en file. Le worker re-rend maintenant au depilage avec cette meme
// fonction (version COURANTE du modele, donnees COURANTES du contact), donc
// aucune divergence possible entre ce qui est valide a l'enqueue et ce qui part.

// automationEmailRenderInput regroupe tout ce qui entre dans le rendu.
type automationEmailRenderInput struct {
	Template       *domain.Template
	Workspace      *domain.Workspace
	APIEndpoint    string
	AutomationID   string
	AutomationName string
	ListID         string
	ListName       string
	TemplateID     string
	MessageID      string
	Contact        *domain.Contact
}

// automationEmailRender est le contenu rendu, pret a etre mis dans un payload.
type automationEmailRender struct {
	Subject       string
	HTML          string
	Text          string
	PlainTextOnly bool
	ReplyTo       string
	SenderID      string
	TemplateData  domain.MapOfAny
}

// renderAutomationEmail compile le modele (MJML + Liquid) pour un contact.
// Les messages d'erreur sont ceux de l'ancien code inline du noeud email.
func renderAutomationEmail(in automationEmailRenderInput) (*automationEmailRender, error) {
	if in.Template == nil || in.Workspace == nil || in.Contact == nil {
		return nil, fmt.Errorf("render input incomplete (template, workspace and contact are required)")
	}

	endpoint := in.APIEndpoint
	if in.Workspace.Settings.CustomEndpointURL != nil && *in.Workspace.Settings.CustomEndpointURL != "" {
		endpoint = *in.Workspace.Settings.CustomEndpointURL
	}

	trackingSettings := notifuse_mjml.TrackingSettings{
		Endpoint:       endpoint,
		EnableTracking: in.Workspace.Settings.EmailTrackingEnabled,
		UTMSource:      "automation",
		UTMMedium:      "email",
		UTMCampaign:    in.AutomationName,
		UTMContent:     in.TemplateID,
		WorkspaceID:    in.Workspace.ID,
		MessageID:      in.MessageID,
	}

	templateData, err := domain.BuildTemplateData(domain.TemplateDataRequest{
		WorkspaceID:         in.Workspace.ID,
		WorkspaceSecretKey:  in.Workspace.Settings.SecretKey,
		WorkspaceWebsiteURL: in.Workspace.Settings.WebsiteURL,
		ContactWithList:     domain.ContactWithList{Contact: in.Contact, ListID: in.ListID, ListName: in.ListName},
		MessageID:           in.MessageID,
		TrackingSettings:    trackingSettings,
		ProvidedData: domain.MapOfAny{
			"automation_id":   in.AutomationID,
			"automation_name": in.AutomationName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build template data: %w", err)
	}

	// Resolve language variant based on contact's language
	contactLang := ""
	if in.Contact.Language != nil && !in.Contact.Language.IsNull {
		contactLang = in.Contact.Language.String
	}
	emailContent := in.Template.ResolveEmailContent(contactLang, in.Workspace.Settings.DefaultLanguage)
	if emailContent == nil {
		return nil, fmt.Errorf("template %s has no email content", in.TemplateID)
	}

	compileReq := notifuse_mjml.CompileTemplateRequest{
		WorkspaceID:      in.Workspace.ID,
		MessageID:        in.MessageID,
		VisualEditorTree: emailContent.VisualEditorTree,
		TemplateData:     notifuse_mjml.MapOfAny(templateData),
		TrackingSettings: trackingSettings,
	}
	compileReq.MjmlSource = emailContent.GetCodeModeMjmlSource()
	compiledTemplate, err := notifuse_mjml.CompileTemplate(compileReq)
	if err != nil {
		return nil, fmt.Errorf("failed to compile template: %w", err)
	}
	if !compiledTemplate.Success || compiledTemplate.HTML == nil {
		errMsg := "template compilation failed"
		if compiledTemplate.Error != nil {
			errMsg = compiledTemplate.Error.Message
		}
		return nil, fmt.Errorf("%s", errMsg)
	}
	htmlContent := *compiledTemplate.HTML

	textContent := ""
	if emailContent.Text != nil {
		textContent, err = notifuse_mjml.ProcessLiquidTemplate(*emailContent.Text, templateData, "email_text")
		if err != nil {
			return nil, fmt.Errorf("failed to process plain text: %w", err)
		}
		textContent = veridian_spintax.ResolveSpintax(textContent, in.Contact.Email)
	}

	subject, err := notifuse_mjml.ProcessLiquidTemplate(emailContent.Subject, templateData, "email_subject")
	if err != nil {
		return nil, fmt.Errorf("failed to process subject: %w", err)
	}

	return &automationEmailRender{
		Subject:       subject,
		HTML:          htmlContent,
		Text:          textContent,
		PlainTextOnly: emailContent.PlainTextOnly,
		ReplyTo:       emailContent.ReplyTo,
		SenderID:      emailContent.SenderID,
		TemplateData:  templateData,
	}, nil
}
