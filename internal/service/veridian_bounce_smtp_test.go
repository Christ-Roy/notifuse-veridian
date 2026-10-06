package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Constat 06/10/2026, quatre rejets reels de la campagne : seule la premiere adresse
// (cotentin, 5.1.10) etait vraiment morte. gizeh (5.7.133, politique), abh (5.2.2, boite
// pleine) et dallmayr (5.4.14, routage) avaient ete supprimes a tort. En plus, aucun NDR
// n'etait rattache a son envoi (bounced_at restait vide) : le NDR cite `uuid@domaine`,
// message_history.id vaut `robertbrunon_<uuid>`.

const smtpWS, smtpInt = "robertbrunon", "smtp-int"

func smtpWorkspace() *domain.Workspace {
	return &domain.Workspace{
		ID: smtpWS,
		Integrations: []domain.Integration{
			{ID: smtpInt, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}},
		},
	}
}

func smtpBouncePayload(t *testing.T, messageID, recipient, dsn, diag string) []byte {
	t.Helper()
	raw, err := json.Marshal(domain.SMTPWebhookPayload{
		Event: "bounce", Timestamp: "2026-10-05T06:00:00Z",
		MessageID: messageID, Recipient: recipient, BounceCategory: dsn, DiagnosticCode: diag,
	})
	require.NoError(t, err)
	return raw
}

func internalAuth() domain.InboundWebhookAuth { return domain.InboundWebhookAuth{Internal: true} }

func TestProcessWebhook_SMTP_DeadAddress_5_1_10_HardAndAttachedToItsSend(t *testing.T) {
	service, repo, workspaceRepo, messageHistoryRepo, contactRepo, ctrl := newClassificationTestService(t)
	defer ctrl.Finish()

	const raw = "5b2f6c1e-1d0a-4c1b-9a77-0c8d2f4e9a10@agence-veridian.fr"
	const stored = smtpWS + "_5b2f6c1e-1d0a-4c1b-9a77-0c8d2f4e9a10"

	workspaceRepo.EXPECT().GetByID(gomock.Any(), smtpWS).Return(smtpWorkspace(), nil)
	repo.EXPECT().StoreEvents(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	messageHistoryRepo.EXPECT().ResolveBounceTargetMessageID(gomock.Any(), smtpWS, raw, "contact@cotentin.fr").Return(stored, true, nil)
	messageHistoryRepo.EXPECT().SetStatusesIfNotSet(gomock.Any(), smtpWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, updates []domain.MessageEventUpdate) error {
			require.Len(t, updates, 1)
			assert.Equal(t, stored, updates[0].ID, "le NDR doit etre rattache a l'id stocke, pas au Message-ID brut")
			assert.Equal(t, domain.MessageEventBounced, updates[0].Event)
			require.NotNil(t, updates[0].BounceType)
			assert.Equal(t, domain.VeridianBounceTypeHard, *updates[0].BounceType)
			return nil
		})
	contactRepo.EXPECT().MarkEmailsAsBounced(gomock.Any(), smtpWS, []string{"contact@cotentin.fr"}, gomock.Any()).Return(nil)

	payload := smtpBouncePayload(t, raw, "contact@cotentin.fr", "5.1.10", "smtp; 550 5.1.10 RESOLVER.ADR.RecipientNotFound")
	require.NoError(t, service.ProcessWebhook(context.Background(), smtpWS, smtpInt, payload, internalAuth()))
}

func TestProcessWebhook_SMTP_PolicyRefusal_5_7_133_NotSuppressedButRecorded(t *testing.T) {
	service, repo, workspaceRepo, messageHistoryRepo, _, ctrl := newClassificationTestService(t)
	defer ctrl.Finish()

	const raw = "aaaaaaaa-1d0a-4c1b-9a77-0c8d2f4e9a10@agence-veridian.fr"
	const stored = smtpWS + "_aaaaaaaa-1d0a-4c1b-9a77-0c8d2f4e9a10"

	workspaceRepo.EXPECT().GetByID(gomock.Any(), smtpWS).Return(smtpWorkspace(), nil)
	repo.EXPECT().StoreEvents(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	messageHistoryRepo.EXPECT().ResolveBounceTargetMessageID(gomock.Any(), smtpWS, raw, "info@gizeh.com").Return(stored, true, nil)
	messageHistoryRepo.EXPECT().SetStatusesIfNotSet(gomock.Any(), smtpWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, updates []domain.MessageEventUpdate) error {
			require.Len(t, updates, 1)
			assert.Equal(t, stored, updates[0].ID)
			assert.Equal(t, domain.MessageEventPolicyRefused, updates[0].Event, "refus de politique : pas de bounced_at")
			require.NotNil(t, updates[0].BounceType)
			assert.Equal(t, domain.VeridianBounceTypePolicy, *updates[0].BounceType)
			return nil
		})
	// Ni CountConsecutiveSoftBounces ni MarkEmailsAsBounced : aucun EXPECT, gomock echoue
	// des qu'un des deux est appele (le contact n'est PAS supprime).

	payload := smtpBouncePayload(t, raw, "info@gizeh.com", "5.7.133", "smtp; 550 5.7.133 RESOLVER.RST.SenderNotAuthenticatedForGroup")
	require.NoError(t, service.ProcessWebhook(context.Background(), smtpWS, smtpInt, payload, internalAuth()))
}

func TestProcessWebhook_SMTP_MailboxFull_5_2_2_SoftNotSuppressed(t *testing.T) {
	service, repo, workspaceRepo, messageHistoryRepo, _, ctrl := newClassificationTestService(t)
	defer ctrl.Finish()

	workspaceRepo.EXPECT().GetByID(gomock.Any(), smtpWS).Return(smtpWorkspace(), nil)
	repo.EXPECT().StoreEvents(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	// Soft : aucun rattachement a un envoi, aucune resolution.
	messageHistoryRepo.EXPECT().SetStatusesIfNotSet(gomock.Any(), smtpWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, updates []domain.MessageEventUpdate) error {
			assert.Empty(t, updates)
			return nil
		})
	repo.EXPECT().CountConsecutiveSoftBounces(gomock.Any(), smtpWS, []string{"x@abh.fr"}).
		Return(map[string]int{"x@abh.fr": 1}, nil)

	payload := smtpBouncePayload(t, "bbbbbbbb-1d0a-4c1b-9a77-0c8d2f4e9a10@d.fr", "x@abh.fr", "5.2.2", "smtp; 552 5.2.2 mailbox full")
	require.NoError(t, service.ProcessWebhook(context.Background(), smtpWS, smtpInt, payload, internalAuth()))
}

func TestProcessWebhook_SMTP_Routing_5_4_14_SoftEscalatesOnlyAtThreshold(t *testing.T) {
	service, repo, workspaceRepo, messageHistoryRepo, contactRepo, ctrl := newClassificationTestService(t)
	defer ctrl.Finish()

	workspaceRepo.EXPECT().GetByID(gomock.Any(), smtpWS).Return(smtpWorkspace(), nil)
	repo.EXPECT().StoreEvents(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	messageHistoryRepo.EXPECT().SetStatusesIfNotSet(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	// Message-ID vide dans le NDR (cas dallmayr) : le classement n'en depend pas.
	repo.EXPECT().CountConsecutiveSoftBounces(gomock.Any(), smtpWS, []string{"a@dallmayr.com"}).
		Return(map[string]int{"a@dallmayr.com": domain.DefaultSoftBounceThreshold}, nil)
	contactRepo.EXPECT().MarkEmailsAsBounced(gomock.Any(), smtpWS, []string{"a@dallmayr.com"}, gomock.Any()).Return(nil)

	payload := smtpBouncePayload(t, "", "a@dallmayr.com", "5.4.14", "smtp; 554 5.4.14 Hop count exceeded")
	require.NoError(t, service.ProcessWebhook(context.Background(), smtpWS, smtpInt, payload, internalAuth()))
}

func TestProcessWebhook_SMTP_HardBounceWithEmptyMessageID_FallsBackToLastSend(t *testing.T) {
	service, repo, workspaceRepo, messageHistoryRepo, contactRepo, ctrl := newClassificationTestService(t)
	defer ctrl.Finish()

	const lastSend = smtpWS + "_cccccccc-1d0a-4c1b-9a77-0c8d2f4e9a10"

	workspaceRepo.EXPECT().GetByID(gomock.Any(), smtpWS).Return(smtpWorkspace(), nil)
	repo.EXPECT().StoreEvents(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	messageHistoryRepo.EXPECT().ResolveBounceTargetMessageID(gomock.Any(), smtpWS, "", "gone@dead.fr").Return(lastSend, true, nil)
	messageHistoryRepo.EXPECT().SetStatusesIfNotSet(gomock.Any(), smtpWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, updates []domain.MessageEventUpdate) error {
			require.Len(t, updates, 1)
			assert.Equal(t, lastSend, updates[0].ID)
			return nil
		})
	contactRepo.EXPECT().MarkEmailsAsBounced(gomock.Any(), smtpWS, []string{"gone@dead.fr"}, gomock.Any()).Return(nil)

	payload := smtpBouncePayload(t, "", "gone@dead.fr", "5.1.1", "smtp; 550 5.1.1 User unknown")
	require.NoError(t, service.ProcessWebhook(context.Background(), smtpWS, smtpInt, payload, internalAuth()))
}

func TestProcessWebhook_SMTP_HardBounceUnresolvable_StillSuppressesContact(t *testing.T) {
	service, repo, workspaceRepo, messageHistoryRepo, contactRepo, ctrl := newClassificationTestService(t)
	defer ctrl.Finish()

	workspaceRepo.EXPECT().GetByID(gomock.Any(), smtpWS).Return(smtpWorkspace(), nil)
	repo.EXPECT().StoreEvents(gomock.Any(), smtpWS, gomock.Any()).Return(nil)
	messageHistoryRepo.EXPECT().ResolveBounceTargetMessageID(gomock.Any(), smtpWS, "", "gone@dead.fr").Return("", false, nil)
	messageHistoryRepo.EXPECT().SetStatusesIfNotSet(gomock.Any(), smtpWS, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, updates []domain.MessageEventUpdate) error {
			assert.Empty(t, updates)
			return nil
		})
	contactRepo.EXPECT().MarkEmailsAsBounced(gomock.Any(), smtpWS, []string{"gone@dead.fr"}, gomock.Any()).Return(nil)

	payload := smtpBouncePayload(t, "", "gone@dead.fr", "5.1.1", "smtp; 550 5.1.1 User unknown")
	require.NoError(t, service.ProcessWebhook(context.Background(), smtpWS, smtpInt, payload, internalAuth()))
}
