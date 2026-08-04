package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspaceWithSMTP construit un workspace fixture avec une intégration d'envoi
// SMTP (kind=smtp) sous l'ID donné, plus une intégration IMAP de réception (pour
// vérifier que le consumer ne se trompe pas de cible).
func workspaceWithSMTP(workspaceID, smtpIntegrationID string) *domain.Workspace {
	return &domain.Workspace{
		ID:   workspaceID,
		Name: "Cold WS",
		Integrations: domain.Integrations{
			{
				ID:           "imap-recv-1",
				Name:         "Bounce mailbox",
				Type:         domain.IntegrationTypeIMAP,
				IMAPSettings: &domain.IMAPSettings{Host: "imap.host", Port: 993, Username: "u"},
			},
			{
				ID:            smtpIntegrationID,
				Name:          "Postfix relay",
				Type:          domain.IntegrationTypeEmail,
				EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP},
			},
		},
	}
}

// postfixHardNDRRaw : un NDR Postfix hard (550 5.1.1) minimal mais réaliste.
const postfixHardNDRRaw = "From: MAILER-DAEMON@mail.agences-veridian.fr (Mail Delivery System)\r\n" +
	"To: bounce@agences-veridian.fr\r\n" +
	"Subject: Undelivered Mail Returned to Sender\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/report; report-type=delivery-status; boundary=\"B1\"\r\n" +
	"\r\n" +
	"--B1\r\n" +
	"Content-Type: text/plain\r\n\r\nDelivery failed.\r\n" +
	"--B1\r\n" +
	"Content-Type: message/delivery-status\r\n\r\n" +
	"Final-Recipient: rfc822; mort@client-disparu.fr\r\n" +
	"Action: failed\r\n" +
	"Status: 5.1.1\r\n" +
	"Diagnostic-Code: smtp; 550 5.1.1 User unknown\r\n" +
	"--B1--\r\n"

func TestVeridianBounceConsumer_Name(t *testing.T) {
	c := NewVeridianBounceConsumer(nil, nil, logger.NewLogger())
	assert.Equal(t, "bounce-loop", c.Name())
}

func TestVeridianBounceConsumer_HardBounce_TriggersSuppression(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	const wsID = "ws-cold-1"
	const smtpID = "smtp-postfix-1"

	wsRepo.EXPECT().GetByID(gomock.Any(), wsID).Return(workspaceWithSMTP(wsID, smtpID), nil)

	// ProcessWebhook doit être appelé avec l'ID de l'intégration SMTP (PAS l'IMAP)
	// et un payload SMTP bounce correctement formé (recipient + code DSN).
	webhookSvc.EXPECT().
		ProcessWebhook(gomock.Any(), wsID, smtpID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, raw []byte) error {
			var p domain.SMTPWebhookPayload
			require.NoError(t, json.Unmarshal(raw, &p))
			assert.Equal(t, "bounce", p.Event)
			assert.Equal(t, "mort@client-disparu.fr", p.Recipient)
			assert.Equal(t, "5.1.1", p.BounceCategory) // code DSN -> Hard via ClassifyBounce
			assert.Contains(t, p.DiagnosticCode, "User unknown")
			assert.NotEmpty(t, p.Timestamp)
			return nil
		})

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())

	msg := &domain.VeridianIMAPMessage{
		UID:           42,
		WorkspaceID:   wsID,
		IntegrationID: "imap-recv-1", // le poller donne l'ID de la boîte IMAP
		From:          "MAILER-DAEMON@mail.agences-veridian.fr",
		Subject:       "Undelivered Mail Returned to Sender",
		RawBody:       []byte(postfixHardNDRRaw),
	}
	assert.NoError(t, c.OnNewMessage(msg))
}

func TestVeridianBounceConsumer_NotNDR_Ignored(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	// Aucune attente : ni GetByID ni ProcessWebhook ne doivent être appelés.

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())

	reply := "From: Jean Client <jean@vraie-entreprise.fr>\r\n" +
		"Subject: Re: Votre proposition\r\n\r\n" +
		"Bonjour, votre offre m'intéresse, rappelez-moi.\r\n"
	msg := &domain.VeridianIMAPMessage{
		UID:         7,
		WorkspaceID: "ws-cold-1",
		From:        "jean@vraie-entreprise.fr",
		Subject:     "Re: Votre proposition",
		RawBody:     []byte(reply),
	}
	assert.NoError(t, c.OnNewMessage(msg), "une vraie réponse ne doit déclencher aucune suppression")
}

func TestVeridianBounceConsumer_Idempotent_SameNDRTwice(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	const wsID = "ws-cold-1"
	const smtpID = "smtp-postfix-1"

	// Rejouer le MÊME NDR 2x : le consumer ré-appelle ProcessWebhook 2x (il ne
	// tient pas d'état), mais ne plante jamais. L'idempotence RÉELLE de la
	// suppression est garantie en aval par MarkEmailsAsBounced (UPDATE ... WHERE
	// status NOT IN ('complained','bounced')). On valide ici que le consumer est
	// stable et déterministe sur un rejeu.
	wsRepo.EXPECT().GetByID(gomock.Any(), wsID).Return(workspaceWithSMTP(wsID, smtpID), nil).Times(2)
	webhookSvc.EXPECT().ProcessWebhook(gomock.Any(), wsID, smtpID, gomock.Any()).Return(nil).Times(2)

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())
	msg := &domain.VeridianIMAPMessage{
		UID:         42,
		WorkspaceID: wsID,
		From:        "MAILER-DAEMON@mail.agences-veridian.fr",
		Subject:     "Undelivered Mail Returned to Sender",
		RawBody:     []byte(postfixHardNDRRaw),
	}
	assert.NoError(t, c.OnNewMessage(msg))
	assert.NoError(t, c.OnNewMessage(msg))
}

func TestVeridianBounceConsumer_NoSMTPIntegration_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	const wsID = "ws-no-smtp"
	// Workspace SANS intégration SMTP (que de l'IMAP).
	wsRepo.EXPECT().GetByID(gomock.Any(), wsID).Return(&domain.Workspace{
		ID: wsID,
		Integrations: domain.Integrations{
			{ID: "imap-1", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{}},
		},
	}, nil)
	// ProcessWebhook ne doit PAS être appelé (pas de cible).

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: wsID,
		From:        "MAILER-DAEMON@host",
		Subject:     "Undelivered Mail Returned to Sender",
		RawBody:     []byte(postfixHardNDRRaw),
	}
	err := c.OnNewMessage(msg)
	require.Error(t, err, "best-effort : erreur retournée (le poller marque vu quand même)")
	assert.Contains(t, err.Error(), "no SMTP")
}

func TestVeridianBounceConsumer_WorkspaceLookupError_Propagated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().GetByID(gomock.Any(), "ws-x").Return(nil, errors.New("db down"))

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: "ws-x",
		From:        "MAILER-DAEMON@host",
		Subject:     "Undelivered Mail Returned to Sender",
		RawBody:     []byte(postfixHardNDRRaw),
	}
	err := c.OnNewMessage(msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestVeridianBounceConsumer_ProcessWebhookError_Propagated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	const wsID = "ws-cold-1"
	const smtpID = "smtp-1"
	wsRepo.EXPECT().GetByID(gomock.Any(), wsID).Return(workspaceWithSMTP(wsID, smtpID), nil)
	webhookSvc.EXPECT().ProcessWebhook(gomock.Any(), wsID, smtpID, gomock.Any()).Return(errors.New("store failed"))

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: wsID,
		From:        "MAILER-DAEMON@host",
		Subject:     "Undelivered Mail Returned to Sender",
		RawBody:     []byte(postfixHardNDRRaw),
	}
	err := c.OnNewMessage(msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store failed")
}

func TestVeridianBounceConsumer_TransientWebhookErrorSucceedsOnReplay(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	const wsID = "ws-cold-retry"
	const smtpID = "smtp-retry"
	sentinel := errors.New("temporary webhook store outage")

	wsRepo.EXPECT().GetByID(gomock.Any(), wsID).Return(workspaceWithSMTP(wsID, smtpID), nil).Times(2)
	first := webhookSvc.EXPECT().ProcessWebhook(gomock.Any(), wsID, smtpID, gomock.Any()).Return(sentinel)
	webhookSvc.EXPECT().ProcessWebhook(gomock.Any(), wsID, smtpID, gomock.Any()).Return(nil).After(first)

	consumer := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())
	msg := &domain.VeridianIMAPMessage{
		UID:         84,
		WorkspaceID: wsID,
		From:        "MAILER-DAEMON@host",
		Subject:     "Undelivered Mail Returned to Sender",
		RawBody:     []byte(postfixHardNDRRaw),
	}
	require.ErrorIs(t, consumer.OnNewMessage(msg), sentinel)
	require.NoError(t, consumer.OnNewMessage(msg), "the same unacknowledged UID must succeed after a transient dependency recovers")
}

func TestVeridianBounceConsumer_NilMessageAndDeps(t *testing.T) {
	c := NewVeridianBounceConsumer(nil, nil, logger.NewLogger())
	assert.NoError(t, c.OnNewMessage(nil), "message nil = no-op")

	// Message valide mais deps nil = no-op loggué, jamais de panic.
	msg := &domain.VeridianIMAPMessage{RawBody: []byte(postfixHardNDRRaw)}
	assert.NoError(t, c.OnNewMessage(msg))
}

func TestVeridianBounceConsumer_SoftBounce_StillProcessed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	webhookSvc := mocks.NewMockInboundWebhookEventServiceInterface(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	const wsID = "ws-cold-1"
	const smtpID = "smtp-1"
	wsRepo.EXPECT().GetByID(gomock.Any(), wsID).Return(workspaceWithSMTP(wsID, smtpID), nil)

	softNDR := "From: postmaster@outlook.com\r\nSubject: Undeliverable\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=\"S\"\r\n\r\n" +
		"--S\r\nContent-Type: message/delivery-status\r\n\r\n" +
		"Final-Recipient: rfc822;plein@contoso.com\r\nStatus: 4.2.2\r\n" +
		"Diagnostic-Code: smtp;452 4.2.2 over quota\r\n--S--\r\n"

	webhookSvc.EXPECT().
		ProcessWebhook(gomock.Any(), wsID, smtpID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, raw []byte) error {
			var p domain.SMTPWebhookPayload
			require.NoError(t, json.Unmarshal(raw, &p))
			assert.Equal(t, "plein@contoso.com", p.Recipient)
			assert.Equal(t, "4.2.2", p.BounceCategory) // soft -> compte vers seuil
			return nil
		})

	c := NewVeridianBounceConsumer(webhookSvc, wsRepo, logger.NewLogger())
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: wsID,
		From:        "postmaster@outlook.com",
		Subject:     "Undeliverable",
		RawBody:     []byte(softNDR),
	}
	assert.NoError(t, c.OnNewMessage(msg))
}
