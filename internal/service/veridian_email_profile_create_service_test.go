package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const createTestSecretKey = "test-secret-key-for-profiles-0001"

func newCreateServiceForTest(t *testing.T) (domain.VeridianEmailProfileCreateService, *mocks.MockWorkspaceRepository, *mocks.MockAuthService) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	auth := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianEmailProfileCreateService(repo, auth, createTestSecretKey, log), repo, auth
}

func expectOwner(auth *mocks.MockAuthService, repo *mocks.MockWorkspaceRepository, role string) {
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{ID: "u1"}, &domain.UserWorkspace{}, nil)
	repo.EXPECT().GetUserWorkspace(gomock.Any(), "u1", "ws1").Return(&domain.UserWorkspace{UserID: "u1", WorkspaceID: "ws1", Role: role}, nil)
}

func gmailRequest() domain.VeridianCreateEmailProfileRequest {
	return domain.VeridianCreateEmailProfileRequest{
		WorkspaceID: "ws1", Type: domain.VeridianCreateProfileTypeGmailAppPassword,
		Name: "Gmail Robert", SenderEmail: "Robert@Gmail.com", SenderName: "Robert",
		AppPassword: "abcd efgh ijkl mnop",
	}
}

func TestVeridianBuildProfileFromRequestGmail(t *testing.T) {
	provider, imap, err := veridianBuildProfileFromRequest(gmailRequest())
	require.NoError(t, err)
	assert.Equal(t, "smtp.gmail.com", provider.SMTP.Host)
	assert.Equal(t, 587, provider.SMTP.Port)
	assert.Equal(t, "robert@gmail.com", provider.SMTP.Username)
	assert.Equal(t, "abcdefghijklmnop", provider.SMTP.Password, "les espaces du mot de passe d'application sont retirés")
	assert.Equal(t, 30, provider.VeridianProfileDailyCap, "plafond Gmail par défaut")
	assert.Equal(t, 1, provider.RateLimitPerMinute)
	require.NotNil(t, imap)
	assert.Equal(t, "imap.gmail.com", imap.Host)
	assert.Equal(t, 993, imap.Port)
	assert.True(t, imap.UseTLS)
	assert.Equal(t, "robert@gmail.com", imap.Username)
	assert.Equal(t, "abcdefghijklmnop", imap.Password)
}

func TestVeridianBuildProfileFromRequestSMTPIMAP(t *testing.T) {
	req := domain.VeridianCreateEmailProfileRequest{
		WorkspaceID: "ws1", Type: domain.VeridianCreateProfileTypeSMTPIMAP,
		SenderEmail: "a@exemple.fr", SMTP: &domain.VeridianCreateProfileSMTP{Host: "smtp.exemple.fr", Port: 465, UseTLS: true, Username: "a", Password: "pw"},
	}
	provider, imap, err := veridianBuildProfileFromRequest(req)
	require.NoError(t, err)
	assert.Nil(t, imap, "IMAP facultatif")
	assert.Equal(t, 60, provider.RateLimitPerMinute)
	req.IMAP = &domain.VeridianCreateProfileIMAP{Host: "imap.exemple.fr", Port: 993, UseTLS: true, Username: "a", Password: "pw"}
	_, imap, err = veridianBuildProfileFromRequest(req)
	require.NoError(t, err)
	assert.Equal(t, "INBOX", imap.Folder)
}

func TestVeridianCreateEmailProfileService(t *testing.T) {
	t.Run("gmail: SMTP et IMAP crees et lies d'un seul Update, secret chiffre et jamais renvoye", func(t *testing.T) {
		svc, repo, auth := newCreateServiceForTest(t)
		expectOwner(auth, repo, "owner")
		repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{ID: "ws1"}, nil)
		var saved *domain.Workspace
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1).DoAndReturn(func(_ context.Context, w *domain.Workspace) error {
			saved = w
			return nil
		})

		result, err := svc.CreateEmailProfile(context.Background(), gmailRequest())
		require.NoError(t, err)
		require.NotNil(t, saved)
		require.Len(t, saved.Integrations, 2)

		var email, inbox *domain.Integration
		for i := range saved.Integrations {
			switch saved.Integrations[i].Type {
			case domain.IntegrationTypeEmail:
				email = &saved.Integrations[i]
			case domain.IntegrationTypeIMAP:
				inbox = &saved.Integrations[i]
			}
		}
		require.NotNil(t, email)
		require.NotNil(t, inbox)
		assert.Equal(t, result.IntegrationID, email.ID)
		assert.Equal(t, result.IMAPIntegrationID, inbox.ID)
		assert.Equal(t, inbox.ID, email.EmailProvider.VeridianReturnIMAPIntegrationID, "le lien est posé dans la même écriture")
		assert.Nil(t, email.EmailProvider.VeridianTransportVerifiedAt, "non vérifié tant que le test n'a pas réussi")
		assert.NotEmpty(t, email.EmailProvider.SMTP.EncryptedPassword)
		assert.NotEmpty(t, inbox.IMAPSettings.EncryptedPassword)
		assert.Empty(t, saved.Settings.VeridianMarketingEmailProviderIDs, "le profil naît hors rotation")
		assert.Empty(t, saved.Settings.TransactionalEmailProviderID)

		body, err := json.Marshal(result)
		require.NoError(t, err)
		assert.False(t, strings.Contains(string(body), "abcdefgh"), "le secret n'est jamais renvoyé")
	})

	t.Run("refus pour un non-proprietaire", func(t *testing.T) {
		svc, repo, auth := newCreateServiceForTest(t)
		expectOwner(auth, repo, "member")
		_, err := svc.CreateEmailProfile(context.Background(), gmailRequest())
		var unauthorized *domain.ErrUnauthorized
		require.ErrorAs(t, err, &unauthorized)
	})

	t.Run("mot de passe d'application de mauvaise longueur: rien n'est ecrit", func(t *testing.T) {
		svc, repo, auth := newCreateServiceForTest(t)
		expectOwner(auth, repo, "owner")
		repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{ID: "ws1"}, nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
		req := gmailRequest()
		req.AppPassword = "trop-court"
		_, err := svc.CreateEmailProfile(context.Background(), req)
		var validation domain.ValidationError
		require.ErrorAs(t, err, &validation)
	})

	t.Run("plafond hors limite Gmail personnel: rien n'est ecrit", func(t *testing.T) {
		svc, repo, auth := newCreateServiceForTest(t)
		expectOwner(auth, repo, "owner")
		repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{ID: "ws1"}, nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
		req := gmailRequest()
		req.ProfileDailyCap = 5000
		_, err := svc.CreateEmailProfile(context.Background(), req)
		require.Error(t, err)
	})

	t.Run("une erreur d'ecriture est remontee", func(t *testing.T) {
		svc, repo, auth := newCreateServiceForTest(t)
		expectOwner(auth, repo, "owner")
		repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(&domain.Workspace{ID: "ws1"}, nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("db"))
		_, err := svc.CreateEmailProfile(context.Background(), gmailRequest())
		require.Error(t, err)
	})

	t.Run("forme invalide: aucune lecture", func(t *testing.T) {
		svc, _, _ := newCreateServiceForTest(t)
		req := gmailRequest()
		req.Type = "inconnu"
		_, err := svc.CreateEmailProfile(context.Background(), req)
		var validation domain.ValidationError
		require.ErrorAs(t, err, &validation)
	})
}
