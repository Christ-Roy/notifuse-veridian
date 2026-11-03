// Package smtprelay transforme le bridge SMTP en relais de soumission : une
// application cliente parle SMTP standard, Notifuse envoie le mail par le profil
// transactionnel reserve du workspace. Design : docs/claude/71-relais-smtp-de-soumission.md.
//
// Le paquet ne depend d'aucun paquet applicatif concret : ses dependances passent
// par des ports (Directory, Suppression, Mailer, History, Limiter), branches dans
// internal/app/veridian_wiring.go.
package smtprelay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
)

// Limites et identifiants du relais.
const (
	MaxMessageBytes = 10 * 1024 * 1024 // aligne sur le bridge (10 Mo)
	MaxRecipients   = 50               // aligne sur le bridge
	RateNamespace   = "smtp_relay"     // namespace du ratelimiter global
	RatePerMinute   = 60               // messages par minute et par cle
	RelayTemplateID = "relay"          // sentinelle message_history (template_id NOT NULL, sans FK)
	dedupeWindow    = 24 * time.Hour
)

// Directory resout la cle API vers ses workspaces et lit un workspace.
type Directory interface {
	UserWorkspaceIDs(ctx context.Context, userID string) ([]string, error)
	Workspace(ctx context.Context, workspaceID string) (*domain.Workspace, error)
}

// Suppression rend les statuts de listes du contact dans le workspace (vide si inconnu).
type Suppression interface {
	ContactListStatuses(ctx context.Context, workspaceID, email string) ([]domain.ContactListStatus, error)
}

// Mailer envoie un message via un profil (EmailService.SendEmail en production).
type Mailer interface {
	SendEmail(ctx context.Context, req domain.SendEmailProviderRequest, isMarketing bool) error
}

// History ecrit la ligne message_history (MessageHistoryRepository.Create).
type History interface {
	Create(ctx context.Context, workspaceID string, secretKey string, message *domain.MessageHistory) error
}

// Limiter est le ratelimiter global (Allow consomme une tentative).
type Limiter interface {
	Allow(namespace, key string) bool
}

// Deps regroupe les ports. Now est optionnel (time.Now par defaut).
type Deps struct {
	Directory   Directory
	Suppression Suppression
	Mailer      Mailer
	History     History
	Limiter     Limiter
	Logger      logger.Logger
	Now         func() time.Time
}

// Relay soumet un message SMTP au profil transactionnel reserve du workspace.
type Relay struct {
	deps   Deps
	dedupe *Dedupe
}

// New construit le relais. Les ports doivent etre renseignes.
func New(d Deps) *Relay {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Relay{deps: d, dedupe: NewDedupe(dedupeWindow)}
}

// Submit traite un message non-JSON recu par le bridge. Chaque erreur rendue est un
// *smtp.SMTPError : le client voit le code et la cause (pas de perte silencieuse).
func (r *Relay) Submit(ctx context.Context, userID, envelopeFrom string, to []string, data []byte) error {
	log := r.deps.Logger.WithFields(map[string]interface{}{"user_id": userID})
	now := r.deps.Now()

	if !r.deps.Limiter.Allow(RateNamespace, userID) {
		log.Warn("SMTP relay: debit depasse")
		return smtpError(451, 4, 7, 1, "trop de messages, reessayez plus tard")
	}
	if len(to) == 0 {
		return smtpError(554, 5, 5, 0, "aucun destinataire valide")
	}
	if err := CheckSize(len(data)); err != nil {
		return err
	}
	if err := CheckRecipients(len(to)); err != nil {
		return err
	}

	ids, err := r.deps.Directory.UserWorkspaceIDs(ctx, userID)
	if err != nil {
		log.WithField("error", err.Error()).Error("SMTP relay: lecture des workspaces de la cle")
		return smtpError(451, 4, 3, 0, "service indisponible, reessayez")
	}
	workspaceID, err := SingleWorkspace(ids)
	if err != nil {
		return err
	}
	log = log.WithField("workspace_id", workspaceID)

	ws, err := r.deps.Directory.Workspace(ctx, workspaceID)
	if err != nil || ws == nil {
		log.WithField("error", fmt.Sprint(err)).Error("SMTP relay: lecture du workspace")
		return smtpError(451, 4, 3, 0, "service indisponible, reessayez")
	}

	sub, err := ParseSubmission(data)
	if err != nil {
		log.WithField("error", err.Error()).Warn("SMTP relay: message refuse a l'analyse")
		return err
	}

	profileID := ws.VeridianReservedTransactionalProfileID()
	if profileID == "" {
		log.Warn("SMTP relay: aucun profil transactionnel reserve")
		return smtpError(554, 5, 7, 1, "aucun profil transactionnel configure pour ce workspace")
	}
	integration := ws.GetIntegrationByID(profileID)
	if integration == nil {
		return smtpError(554, 5, 7, 1, "profil transactionnel introuvable")
	}
	provider := &integration.EmailProvider

	if err := CheckFrom(sub.HeaderFrom, envelopeFrom, provider.Senders); err != nil {
		log.WithField("from_domain", domainOf(sub.HeaderFrom)).Warn("SMTP relay: expediteur non autorise")
		return err
	}

	key := ""
	if sub.MessageID != "" {
		key = workspaceID + "|" + sub.MessageID
		if r.dedupe.Seen(key, now) {
			log.WithField("message_id", sub.MessageID).Info("SMTP relay: message deja relaye, 250 sans renvoi")
			return nil
		}
	}

	for i, rcpt := range to {
		wireID := uuid.New().String()
		if i == 0 && sub.MessageID != "" {
			wireID = wireLocalPart(sub.MessageID)
		}
		req := buildRequest(workspaceID, profileID, provider, sub, rcpt, wireID)
		if err := r.deps.Mailer.SendEmail(ctx, req, false); err != nil {
			log.WithFields(map[string]interface{}{
				"message_id": wireID,
				"index":      i,
				"error":      err.Error(),
			}).Error("SMTP relay: envoi refuse par le profil")
			return smtpError(451, 4, 4, 1, "envoi temporairement impossible, reessayez")
		}
		r.recordHistory(ctx, ws, sub, rcpt, wireID, now, log)
	}

	if key != "" {
		r.dedupe.Remember(key, now)
	}
	log.WithFields(map[string]interface{}{
		"message_id": sub.MessageID,
		"recipients": len(to),
		"bytes":      len(data),
	}).Info("SMTP relay: message accepte")
	return nil
}

// CheckRecipient est le garde RCPT TO (branche sur le bridge quand le relais est
// actif). Un destinataire bounced ou complained est refuse en 550 5.7.1, les autres
// passent. Si la cle ne designe pas un workspace unique, le garde laisse passer :
// le refus de la cle est tranche par Submit (les messages JSON legacy restent intacts).
func (r *Relay) CheckRecipient(ctx context.Context, userID, to string) error {
	ids, err := r.deps.Directory.UserWorkspaceIDs(ctx, userID)
	if err != nil {
		return smtpError(451, 4, 3, 0, "service indisponible, reessayez")
	}
	workspaceID, err := SingleWorkspace(ids)
	if err != nil {
		return nil
	}
	statuses, err := r.deps.Suppression.ContactListStatuses(ctx, workspaceID, to)
	if err != nil {
		r.deps.Logger.WithField("error", err.Error()).Error("SMTP relay: lecture de la suppression")
		return smtpError(451, 4, 3, 0, "service indisponible, reessayez")
	}
	if IsSuppressed(statuses) {
		r.deps.Logger.WithField("workspace_id", workspaceID).Info("SMTP relay: destinataire en suppression refuse au RCPT")
		return smtpError(550, 5, 7, 1, "destinataire refuse : adresse en suppression")
	}
	return nil
}

// wireLocalPart rend la partie locale d'un Message-ID client : le domaine du
// Message-ID est remplacé par celui de l'expéditeur à l'envoi (VeridianRFCMessageID).
// Garder « uuid@domaine-client » doublerait le domaine (uuid@domaine@domaine).
func wireLocalPart(id string) string {
	if at := strings.IndexByte(id, '@'); at > 0 {
		return id[:at]
	}
	return id
}

// IsSuppressed dit si un contact doit etre refuse par le relais : bounced ou
// complained sur au moins une liste. unsubscribed NE bloque PAS le transactionnel
// (decision main, 02/11, fiche 71).
func IsSuppressed(statuses []domain.ContactListStatus) bool {
	for _, s := range statuses {
		if s == domain.ContactListStatusBounced || s == domain.ContactListStatusComplained {
			return true
		}
	}
	return false
}

// buildRequest compose la requete d'envoi d'un destinataire d'enveloppe.
func buildRequest(workspaceID, profileID string, provider *domain.EmailProvider, sub *Submission, rcpt, wireID string) domain.SendEmailProviderRequest {
	subject := sub.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	fromName := sub.FromName
	if fromName == "" {
		fromName = sub.HeaderFrom
	}
	content := sub.HTML
	if content == "" {
		content = textAsHTML(sub.Text)
	}
	return domain.SendEmailProviderRequest{
		WorkspaceID:   workspaceID,
		IntegrationID: profileID,
		MessageID:     wireID,
		FromAddress:   sub.HeaderFrom,
		FromName:      fromName,
		To:            rcpt,
		Subject:       subject,
		Content:       content,
		TextContent:   sub.Text,
		Provider:      provider,
		EmailOptions: domain.EmailOptions{
			ReplyTo:            sub.ReplyTo,
			InReplyTo:          sub.InReplyTo,
			References:         sub.References,
			ListUnsubscribeURL: sub.ListUnsubscribeURL,
			Attachments:        sub.Attachments,
		},
	}
}

// recordHistory ecrit la ligne message_history. Le mail est deja parti : un echec
// d'ecriture est journalise, il ne change pas la reponse SMTP.
func (r *Relay) recordHistory(ctx context.Context, ws *domain.Workspace, sub *Submission, rcpt, wireID string, now time.Time, log logger.Logger) {
	var externalID *string
	if sub.MessageID != "" {
		id := sub.MessageID
		externalID = &id
	}
	sentAt := now.UTC()
	msg := &domain.MessageHistory{
		ID:                  wireID,
		ExternalID:          externalID,
		ContactEmail:        rcpt,
		TemplateID:          RelayTemplateID,
		TemplateVersion:     0,
		Channel:             "email",
		MessageData:         domain.MessageData{Data: map[string]interface{}{}},
		SentAt:              &sentAt,
		CreatedAt:           sentAt,
		UpdatedAt:           sentAt,
		VeridianMessageType: "transactional",
	}
	if err := r.deps.History.Create(ctx, ws.ID, ws.Settings.SecretKey, msg); err != nil {
		log.WithField("message_id", wireID).WithField("error", err.Error()).Warn("SMTP relay: historique non ecrit (mail parti)")
	}
}

// smtpError construit l'erreur rendue au client par le bridge.
func smtpError(code, c1, c2, c3 int, msg string) *smtp.SMTPError {
	return &smtp.SMTPError{
		Code:         code,
		EnhancedCode: smtp.EnhancedCode{c1, c2, c3},
		Message:      msg,
	}
}
