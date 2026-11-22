// Package bounces applique un rapport de non-remise lu par la boîte unifiée (VER-220, 22/11/2026).
//
// Il ne réinvente rien de la chaîne existante (fiche 17) : le rebond passe par
// InboundWebhookEventService.ProcessWebhook, qui classe le code DSN (ClassifyBounce), pose
// bounced_at et bounce_type sur l'envoi, et passe le contact en « bounced » dans ses listes pour
// un rebond dur. Ce paquet y ajoute ce que la chaîne ne faisait pas : SORTIR le contact de ses
// séquences en cours (inscriptions « active » et « sending ») sur un rebond dur ou un refus de
// politique, pour qu'aucune relance ne reparte vers une adresse qui a refusé.
package bounces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Motifs de sortie (préfixe « bounce » : classés « rejeté » par VeridianClassifyExitReason).
const (
	ExitReasonHard   = "bounce_hard"
	ExitReasonPolicy = "bounce_policy"
)

// WebhookProcessor : la chaîne existante (InboundWebhookEventService).
type WebhookProcessor interface {
	ProcessWebhook(ctx context.Context, workspaceID, integrationID string, raw []byte, auth domain.InboundWebhookAuth) error
}

// Applier implémente mailbox.BounceSink.
type Applier struct {
	Webhooks    WebhookProcessor
	Workspaces  domain.WorkspaceRepository
	Automations domain.AutomationRepository
	Log         logger.Logger
	Now         func() time.Time
}

var _ mailbox.BounceSink = (*Applier)(nil)

// Outcome : ce que l'application d'un rebond a produit.
type Outcome struct {
	Class  domain.BounceClassification
	Exited int
}

// BounceReceived applique un rebond (mailbox.BounceSink).
func (a *Applier) BounceReceived(ctx context.Context, ws string, b mailbox.Bounce) error {
	_, err := a.Apply(ctx, ws, b)
	return err
}

// Apply applique un rebond et dit ce qu'il a produit.
func (a *Applier) Apply(ctx context.Context, ws string, b mailbox.Bounce) (Outcome, error) {
	recipient := strings.ToLower(strings.TrimSpace(b.Recipient))
	if recipient == "" {
		return Outcome{}, fmt.Errorf("rebond sans adresse en échec")
	}
	integ, err := a.sendingIntegration(ctx, ws, b.ProfileID)
	if err != nil {
		return Outcome{}, err
	}
	msgID := b.SentMessageID
	if msgID == "" {
		msgID = b.OriginalMessageID
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	raw, err := json.Marshal(domain.SMTPWebhookPayload{
		Event: "bounce", Timestamp: now().UTC().Format(time.RFC3339), MessageID: msgID, Recipient: recipient,
		BounceCategory: b.Code, DiagnosticCode: b.Diagnostic, Reason: b.Severity,
	})
	if err != nil {
		return Outcome{}, err
	}
	// Appel interne en Go (jamais HTTP) : Internal:true est la seule exemption de la porte
	// d'authentification des webhooks entrants (domain.InboundWebhookAuth).
	if err := a.Webhooks.ProcessWebhook(ctx, ws, integ, raw, domain.InboundWebhookAuth{Internal: true}); err != nil {
		return Outcome{}, fmt.Errorf("chaîne des rebonds: %w", err)
	}
	out := Outcome{Class: Classify(b.Code, b.Diagnostic)}
	reason := ""
	switch out.Class {
	case domain.BounceClassificationHard:
		reason = ExitReasonHard
	case domain.BounceClassificationPolicyRefusal:
		reason = ExitReasonPolicy
	}
	if reason != "" && a.Automations != nil {
		out.Exited, err = a.exitEnrollments(ctx, ws, recipient, reason)
	}
	if a.Log != nil {
		a.Log.WithFields(map[string]interface{}{
			"workspace_id": ws, "dsn": b.Code, "class": domain.VeridianBounceTypeLabel(out.Class), "exited": out.Exited,
		}).Info("Bounces: bounce applied")
	}
	return out, err
}

// Classify : la classe d'un rebond SMTP, par la règle de la chaîne (domain.ClassifyBounce).
func Classify(code, diagnostic string) domain.BounceClassification {
	return domain.ClassifyBounce(domain.BounceInput{Provider: domain.EmailProviderKindSMTP, Type: "Bounce", Subtype: code, Diagnostic: diagnostic})
}

// sendingIntegration : le profil d'envoi du mail rebondi s'il est SMTP, sinon le premier profil
// SMTP du workspace (ProcessWebhook route sur EmailProvider.Kind).
func (a *Applier) sendingIntegration(ctx context.Context, ws, profileID string) (string, error) {
	w, err := a.Workspaces.GetByID(ctx, ws)
	if err != nil {
		return "", fmt.Errorf("lecture du workspace: %w", err)
	}
	if p := w.GetIntegrationByID(profileID); p != nil && p.Type == domain.IntegrationTypeEmail && p.EmailProvider.Kind == domain.EmailProviderKindSMTP {
		return p.ID, nil
	}
	for _, i := range w.GetIntegrationsByType(domain.IntegrationTypeEmail) {
		if i != nil && i.EmailProvider.Kind == domain.EmailProviderKindSMTP {
			return i.ID, nil
		}
	}
	return "", fmt.Errorf("aucun profil d'envoi SMTP dans le workspace %s", ws)
}

// exitEnrollments sort le contact de ses inscriptions en cours. L'étape et la date restent posées
// (une sortie se défait en remettant le statut) ; seul le statut et le motif changent.
func (a *Applier) exitEnrollments(ctx context.Context, ws, email, reason string) (int, error) {
	cas, _, err := a.Automations.ListContactAutomations(ctx, ws, domain.ContactAutomationFilter{
		ContactEmail: email,
		Status:       []domain.ContactAutomationStatus{domain.ContactAutomationStatusActive, domain.ContactAutomationStatusSending},
		Limit:        1000,
	})
	if err != nil {
		return 0, fmt.Errorf("inscriptions du contact: %w", err)
	}
	n := 0
	var firstErr error
	for _, ca := range cas {
		if ca.Status != domain.ContactAutomationStatusActive && ca.Status != domain.ContactAutomationStatusSending {
			continue
		}
		r := reason
		ca.Status = domain.ContactAutomationStatusExited
		ca.ExitReason = &r
		if err := a.Automations.UpdateContactAutomation(ctx, ws, ca); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("sortie de %s: %w", ca.AutomationID, err)
			}
			continue
		}
		n++
		if err := a.Automations.IncrementAutomationStat(ctx, ws, ca.AutomationID, "exited"); err != nil && a.Log != nil {
			a.Log.WithField("error", err.Error()).Warn("Bounces: exited counter not incremented")
		}
	}
	return n, firstErr
}
