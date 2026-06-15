package service

// Veridian fork — consumer bounce-loop (Lot 2 sprint cold outbound, 2026-06-15).
//
// Ferme la boucle de bounce du cold outbound : le relai SMTP Postfix
// self-hosted renvoie les NDR (Non-Delivery Reports) ASYNCHRONES dans la boîte
// du Return-Path du domaine d'envoi. Le poller IMAP (Lot 1) rapatrie ces
// messages et les dispatche à TOUS les consumers enregistrés ; ce consumer
// détecte les NDR et alimente la chaîne de suppression EXISTANTE de Notifuse —
// il ne réinvente RIEN de la suppression.
//
// Chaîne réutilisée (zéro duplication) :
//
//	NDR brut (RawBody)
//	  └─ veridian_ndr.Parse  → {Recipient, Severity, DSNCode, DiagnosticCode}
//	     └─ domain.SMTPWebhookPayload{Event:"bounce", ...}  (format natif SMTP)
//	        └─ InboundWebhookEventService.ProcessWebhook(...)   [EXISTANT]
//	           └─ processSMTPWebhook → ClassifyBounce → MarkEmailsAsBounced
//	              └─ contact_lists.status='bounced' → plus jamais renvoyé
//
// Le hard/soft est porté par le code DSN (BounceCategory=DSNCode), interprété
// par ClassifyBounce (cas SMTP enrichi en Lot 2 : 5.x.x→Hard, 4.x.x→SoftCount).
//
// Idempotence métier (EXIGÉE par le contrat poller "at-most-once dispatch") :
// MarkEmailsAsBounced est déjà idempotent (UPDATE ... WHERE status NOT IN
// ('complained','bounced')) → rejouer le MÊME NDR ne supprime qu'une fois et
// ne renvoie pas d'erreur. Aucun état supplémentaire à tenir ici.
//
// Best-effort de bout en bout : toute erreur (workspace introuvable, pas
// d'intégration SMTP, ProcessWebhook qui échoue) est loggée et retournée, mais
// le poller marque le message vu quoi qu'il arrive (on ne boucle jamais sur un
// message empoisonné).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/veridian_ndr"
)

// bounceConsumerName est le Name() du consumer (logging / dédup côté poller).
const bounceConsumerName = "bounce-loop"

// bounceProcessTimeout borne le traitement d'un message (résolution workspace +
// ProcessWebhook). Court : le poller appelle OnNewMessage de façon synchrone
// dans sa goroutine, on ne doit pas le bloquer.
const bounceProcessTimeout = 20 * time.Second

// VeridianBounceConsumer implémente domain.VeridianIMAPConsumer. Détecte les
// NDR et les pousse dans la chaîne de suppression via ProcessWebhook.
type VeridianBounceConsumer struct {
	webhookService domain.InboundWebhookEventServiceInterface
	workspaceRepo  domain.WorkspaceRepository
	logger         logger.Logger
}

// NewVeridianBounceConsumer construit le consumer. Toutes les deps sont
// requises ; un appel avec une dep nil dégrade en no-op loggué (jamais de panic).
func NewVeridianBounceConsumer(
	webhookService domain.InboundWebhookEventServiceInterface,
	workspaceRepo domain.WorkspaceRepository,
	log logger.Logger,
) *VeridianBounceConsumer {
	return &VeridianBounceConsumer{
		webhookService: webhookService,
		workspaceRepo:  workspaceRepo,
		logger:         log,
	}
}

// Name identifie le consumer.
func (c *VeridianBounceConsumer) Name() string { return bounceConsumerName }

// OnNewMessage est appelé par le poller IMAP pour chaque message neuf. Parse le
// NDR ; si c'en est un, construit le payload webhook SMTP et le passe à la
// chaîne de suppression existante. Un message non-NDR (vraie réponse de
// prospect, auto-reply...) est ignoré proprement (nil) — le Lot 3 stop-on-reply
// le traitera de son côté.
func (c *VeridianBounceConsumer) OnNewMessage(msg *domain.VeridianIMAPMessage) error {
	if msg == nil {
		return nil
	}
	if c.webhookService == nil || c.workspaceRepo == nil {
		c.logger.Warn("VeridianBounceConsumer: deps nil, skipping message")
		return nil
	}

	log := c.logger.WithFields(map[string]interface{}{
		"consumer":       bounceConsumerName,
		"workspace_id":   msg.WorkspaceID,
		"integration_id": msg.IntegrationID,
		"uid":            msg.UID,
	})

	parsed := veridian_ndr.Parse(msg.RawBody, msg.From, msg.Subject)
	if !parsed.IsNDR {
		// Pas un bounce : on laisse passer (le poller marque vu). Pas une erreur.
		return nil
	}
	if parsed.Recipient == "" {
		// NDR détecté mais sans adresse exploitable : rien à supprimer.
		log.Warn("VeridianBounceConsumer: NDR without extractable recipient, skipping")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), bounceProcessTimeout)
	defer cancel()

	// Résoudre l'intégration d'ENVOI SMTP du workspace : ProcessWebhook route sur
	// EmailProvider.Kind de l'intégration ciblée, donc on doit lui passer l'ID
	// d'une intégration email de kind=smtp (PAS l'intégration IMAP de réception).
	smtpIntegrationID, err := c.resolveSMTPIntegrationID(ctx, msg.WorkspaceID)
	if err != nil {
		log.WithField("error", err.Error()).Warn("VeridianBounceConsumer: cannot resolve SMTP integration, bounce not processed")
		return err
	}

	payload := domain.SMTPWebhookPayload{
		Event:     "bounce",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		MessageID: parsed.OriginalMessageID,
		Recipient: parsed.Recipient,
		// BounceCategory porte le code DSN enrichi : ClassifyBounce (cas SMTP
		// Veridian) en dérive hard (5.x.x) vs soft (4.x.x). C'est ce qui supprime
		// un hard bounce IMMÉDIATEMENT.
		BounceCategory: parsed.DSNCode,
		DiagnosticCode: parsed.DiagnosticCode,
		Reason:         string(parsed.Severity),
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		log.WithField("error", err.Error()).Error("VeridianBounceConsumer: failed to marshal SMTP payload")
		return err
	}

	if err := c.webhookService.ProcessWebhook(ctx, msg.WorkspaceID, smtpIntegrationID, rawPayload); err != nil {
		log.WithFields(map[string]interface{}{
			"error":     err.Error(),
			"recipient": parsed.Recipient,
			"dsn":       parsed.DSNCode,
		}).Warn("VeridianBounceConsumer: ProcessWebhook failed for bounce")
		return err
	}

	log.WithFields(map[string]interface{}{
		"recipient": parsed.Recipient,
		"severity":  string(parsed.Severity),
		"dsn":       parsed.DSNCode,
	}).Info("VeridianBounceConsumer: bounce processed (contact suppression triggered)")
	return nil
}

// resolveSMTPIntegrationID retourne l'ID de la première intégration d'envoi de
// kind=smtp du workspace. ProcessWebhook a besoin d'une intégration dont
// EmailProvider.Kind == "smtp" pour router vers processSMTPWebhook. Erreur si
// aucune (le cold outbound DOIT passer par un provider SMTP — Postfix).
func (c *VeridianBounceConsumer) resolveSMTPIntegrationID(ctx context.Context, workspaceID string) (string, error) {
	workspace, err := c.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		return "", fmt.Errorf("get workspace %s: %w", workspaceID, err)
	}
	for _, integ := range workspace.GetIntegrationsByType(domain.IntegrationTypeEmail) {
		if integ != nil && integ.EmailProvider.Kind == domain.EmailProviderKindSMTP {
			return integ.ID, nil
		}
	}
	return "", fmt.Errorf("no SMTP email integration found in workspace %s", strings.TrimSpace(workspaceID))
}
