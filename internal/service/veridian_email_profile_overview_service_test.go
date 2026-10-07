package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service/queue"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type overviewRepoStub struct {
	rows     []domain.VeridianPlanObservationRow
	counters []domain.VeridianPlanCounterRow
	since    time.Time
	err      error
}

func (s *overviewRepoStub) GetPlanObservations(_ context.Context, _ string, since time.Time) ([]domain.VeridianPlanObservationRow, []domain.VeridianPlanCounterRow, error) {
	s.since = since
	return s.rows, s.counters, s.err
}

func overviewTestWorkspace() *domain.Workspace {
	verified := time.Now().Add(-72 * time.Hour)
	smtp := func(id, name, sender, host string) domain.Integration {
		return domain.Integration{
			ID: id, Name: name, Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 60,
				SMTP:                        &domain.SMTPSettings{Host: host, Port: 587, EncryptedPassword: "chiffre-secret-ne-doit-jamais-sortir"},
				VeridianTransportVerifiedAt: &verified,
				Senders:                     []domain.EmailSender{{ID: "s-" + id, Email: sender, Name: "Veridian", IsDefault: true}},
			},
		}
	}
	nord := smtp("nord", "nord-propre-1", "hello@nord.example", "smtp.nord.example")
	nord.EmailProvider.VeridianProfileDailyCap = 40
	nord.EmailProvider.VeridianReturnIMAPIntegrationID = "imap-nord"
	relai := smtp("relai", "relai-agence-2", "hello@relai.example", "smtp.relai.example")
	relai.EmailProvider.VeridianPaused = true
	relai.EmailProvider.VeridianReturnIMAPIntegrationID = "imap-shared"
	relai2 := smtp("relai2", "relai-agences-n2", "hello@relai2.example", "smtp.relai2.example")
	relai2.EmailProvider.VeridianReturnIMAPIntegrationID = "imap-shared"
	tx := smtp("tx", "transactionnel", "no-reply@tx.example", "smtp.gmail.com")
	return &domain.Workspace{
		ID: "ws1",
		Settings: domain.WorkspaceSettings{
			Timezone:                          "Europe/Paris",
			TransactionalEmailProviderID:      "tx",
			VeridianMarketingEmailProviderIDs: []string{"nord", "relai", "relai2"},
		},
		Integrations: []domain.Integration{
			nord, relai, relai2, tx,
			{ID: "imap-nord", Name: "Return nord", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "imap.nord.example", Username: "retour@nord.example", EncryptedPassword: "chiffre-imap"}},
			{ID: "imap-shared", Name: "Return agences", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "imap.agences.example", Username: "retour@agences.example"}},
			{ID: "imap-global", Name: "Return inbox", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "imap.lark.example", Username: "return@veridian.example"}},
		},
	}
}

func TestBuildVeridianEmailProfilesOverview(t *testing.T) {
	ws := overviewTestWorkspace()
	rows := []domain.VeridianPlanObservationRow{
		{ProfileID: "nord", SenderEmail: "hello@nord.example", ProviderClass: "google", Accepted: 12},
		{ProfileID: "", SenderEmail: "no-reply@tx.example", Accepted: 50},
	}
	counters := []domain.VeridianPlanCounterRow{{Kind: domain.VeridianDailyQuotaKindProfile, Scope: "nord", Used: 12}}
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

	out := buildVeridianEmailProfilesOverview(ws, rows, counters, map[string]queue.VeridianReputationStatus{}, now)

	require.Len(t, out.Profiles, 4)
	byID := map[string]domain.VeridianEmailProfileOverview{}
	for _, p := range out.Profiles {
		byID[p.IntegrationID] = p
	}

	nord := byID["nord"]
	assert.Equal(t, domain.VeridianProfileUsageCommercial, nord.Usage)
	assert.True(t, nord.InRotation)
	assert.True(t, nord.Verified)
	assert.NotNil(t, nord.VerifiedAt)
	assert.True(t, nord.CredentialsConfigured)
	assert.Equal(t, domain.VeridianProfileTypeSMTP, nord.Type)
	require.NotNil(t, nord.ReturnInbox)
	assert.Equal(t, "imap-nord", nord.ReturnInbox.IntegrationID)
	assert.Equal(t, "retour@nord.example", nord.ReturnInbox.Address)
	require.NotNil(t, nord.Plan.DailyCapToday)
	assert.Equal(t, 40, *nord.Plan.DailyCapToday)
	assert.Equal(t, domain.VeridianPlanGateProfileCap, nord.Plan.LimitingGate)
	assert.Equal(t, 12, nord.Plan.SentToday)
	require.NotNil(t, nord.Plan.RemainingToday)
	assert.Equal(t, 28, *nord.Plan.RemainingToday)
	require.Len(t, nord.Senders, 1)
	assert.Equal(t, "hello@nord.example", nord.Senders[0].Email)

	relai := byID["relai"]
	assert.True(t, relai.Paused)
	assert.Contains(t, relai.Plan.BlockedBy, domain.VeridianPlanBlockPaused)
	assert.False(t, relai.Plan.SendableNow)

	tx := byID["tx"]
	assert.Equal(t, domain.VeridianProfileUsageTransactional, tx.Usage)
	assert.Equal(t, domain.VeridianProfileTypeGmailAppPassword, tx.Type)
	assert.False(t, tx.Plan.Applicable)
	assert.Equal(t, 50, tx.Plan.SentToday, "le transactionnel est couvert, avec son volume du jour")
	assert.Nil(t, tx.ReturnInbox)

	// L'IMAP lie a deux profils n'est pas global ; seul Return inbox l'est.
	require.Len(t, out.GlobalInboxes, 1)
	assert.Equal(t, "imap-global", out.GlobalInboxes[0].IntegrationID)
	assert.Empty(t, out.GlobalInboxes[0].LinkedProfiles)
	require.NotNil(t, byID["relai2"].ReturnInbox)
	assert.ElementsMatch(t, []string{"relai", "relai2"}, byID["relai2"].ReturnInbox.LinkedProfiles)

	assert.Equal(t, 12, out.Totals.CommercialSentToday)
	assert.Equal(t, 50, out.Totals.TransactionalSentToday)
	assert.Equal(t, 1, out.Totals.PausedProfiles)
	assert.Equal(t, 2, out.Totals.ActiveCommercialProfiles)
	assert.Nil(t, out.Totals.CommercialCapacityToday, "relai2 n'a aucun plafond : la capacité totale est inconnue, pas 40")
	assert.Empty(t, out.UsageConflicts)
	assert.Equal(t, "2026-10-08", out.Date)

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	for _, secret := range []string{"chiffre-secret-ne-doit-jamais-sortir", "chiffre-imap", "encrypted_password", "\"password\""} {
		assert.NotContains(t, string(raw), secret, "aucun secret dans l'overview")
	}
}

func TestBuildVeridianEmailProfilesOverview_ReportsExclusivityViolationWithoutBreaking(t *testing.T) {
	ws := overviewTestWorkspace()
	ws.Settings.TransactionalEmailProviderID = "nord" // viole la règle : nord est dans le pool
	out := buildVeridianEmailProfilesOverview(ws, nil, nil, nil, time.Now())
	assert.Equal(t, []string{"nord"}, out.UsageConflicts)
	assert.Len(t, out.Profiles, 4, "la lecture ne casse pas")
}

func TestBuildVeridianEmailProfilesOverview_CapacitySumsWhenEveryActiveProfileIsCapped(t *testing.T) {
	ws := overviewTestWorkspace()
	for i := range ws.Integrations {
		if ws.Integrations[i].Type == domain.IntegrationTypeEmail && ws.Integrations[i].ID != "tx" {
			ws.Integrations[i].EmailProvider.VeridianProfileDailyCap = 25
		}
	}
	out := buildVeridianEmailProfilesOverview(ws, nil, nil, nil, time.Now())
	require.NotNil(t, out.Totals.CommercialCapacityToday)
	assert.Equal(t, 50, *out.Totals.CommercialCapacityToday, "relai en pause ne compte pas")
}

func TestVeridianCredentialsConfigured(t *testing.T) {
	assert.True(t, veridianCredentialsConfigured(&domain.EmailProvider{SMTP: &domain.SMTPSettings{EncryptedPassword: "x"}}))
	assert.False(t, veridianCredentialsConfigured(&domain.EmailProvider{SMTP: &domain.SMTPSettings{}}))
	assert.True(t, veridianCredentialsConfigured(&domain.EmailProvider{SMTP: &domain.SMTPSettings{OAuth2Provider: "google", EncryptedOAuth2ClientSecret: "s", EncryptedOAuth2RefreshToken: "r"}}))
	assert.False(t, veridianCredentialsConfigured(&domain.EmailProvider{SMTP: &domain.SMTPSettings{OAuth2Provider: "google", EncryptedOAuth2ClientSecret: "s"}}), "google sans refresh token")
	assert.True(t, veridianCredentialsConfigured(&domain.EmailProvider{SES: &domain.AmazonSESSettings{EncryptedSecretKey: "x"}}))
	assert.True(t, veridianCredentialsConfigured(&domain.EmailProvider{SendGrid: &domain.SendGridSettings{EncryptedAPIKey: "x"}}))
	assert.True(t, veridianCredentialsConfigured(&domain.EmailProvider{Mailjet: &domain.MailjetSettings{EncryptedSecretKey: "x"}}))
	assert.False(t, veridianCredentialsConfigured(&domain.EmailProvider{}))
}

func TestVeridianEmailProfileOverviewService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	auth := mocks.NewMockAuthService(ctrl)
	workspaces := mocks.NewMockWorkspaceRepository(ctrl)
	history := mocks.NewMockMessageHistoryRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	repo := &overviewRepoStub{}
	svc := NewVeridianEmailProfileOverviewService(repo, history, workspaces, auth, log)

	t.Run("workspace requis", func(t *testing.T) {
		_, err := svc.GetEmailProfilesOverview(context.Background(), "")
		require.Error(t, err)
	})

	t.Run("authentification refusee", func(t *testing.T) {
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), nil, nil, errors.New("nope"))
		_, err := svc.GetEmailProfilesOverview(context.Background(), "ws1")
		require.Error(t, err)
	})

	t.Run("lecture de l'historique requise", func(t *testing.T) {
		noRead := &domain.UserWorkspace{Permissions: domain.UserPermissions{domain.PermissionResourceMessageHistory: {Read: false}}}
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, noRead, nil)
		_, err := svc.GetEmailProfilesOverview(context.Background(), "ws1")
		var perm *domain.PermissionError
		require.ErrorAs(t, err, &perm, "doit ressortir en 403 par WriteAuthAwareError")
	})

	t.Run("lecture complete", func(t *testing.T) {
		readOnly := &domain.UserWorkspace{Permissions: domain.UserPermissions{domain.PermissionResourceMessageHistory: {Read: true}}}
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, readOnly, nil)
		workspaces.EXPECT().GetByID(gomock.Any(), "ws1").Return(overviewTestWorkspace(), nil)
		history.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any()).Return(1, nil).AnyTimes()
		history.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
		history.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()
		history.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws1", gomock.Any(), gomock.Any()).Return(map[string]domain.VeridianReputationCounts{}, nil).AnyTimes()

		out, err := svc.GetEmailProfilesOverview(context.Background(), "ws1")
		require.NoError(t, err)
		require.Len(t, out.Profiles, 4)
		assert.Equal(t, 0, repo.since.Hour(), "le jour de politique commence à minuit UTC")
		for _, p := range out.Profiles {
			if p.IntegrationID == "nord" {
				assert.True(t, p.Plan.ReputationAlert, "la plainte vient du même calcul que le fusible")
				assert.Equal(t, 4, p.Plan.DomainSlowdownFactor)
			}
		}
	})

	t.Run("erreur de lecture des compteurs", func(t *testing.T) {
		readOnly := &domain.UserWorkspace{Permissions: domain.UserPermissions{domain.PermissionResourceMessageHistory: {Read: true}}}
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, readOnly, nil)
		workspaces.EXPECT().GetByID(gomock.Any(), "ws1").Return(overviewTestWorkspace(), nil)
		failing := NewVeridianEmailProfileOverviewService(&overviewRepoStub{err: errors.New("db down")}, history, workspaces, auth, log)
		_, err := failing.GetEmailProfilesOverview(context.Background(), "ws1")
		require.Error(t, err)
	})
}

func TestNewVeridianEmailProfileOverviewServiceRetainsDependencies(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	auth := mocks.NewMockAuthService(ctrl)
	workspaces := mocks.NewMockWorkspaceRepository(ctrl)
	history := mocks.NewMockMessageHistoryRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	repo := &overviewRepoStub{}
	concrete, ok := NewVeridianEmailProfileOverviewService(repo, history, workspaces, auth, log).(*veridianEmailProfileOverviewService)
	require.True(t, ok)
	assert.Same(t, repo, concrete.repo)
	assert.Same(t, history, concrete.messageHistoryRepo)
	assert.Same(t, workspaces, concrete.workspaceRepo)
	assert.Same(t, auth, concrete.authService)
}
