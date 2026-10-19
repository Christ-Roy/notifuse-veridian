package service

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

func verifyFixture(t *testing.T) (*EmailService, *mocks.MockWorkspaceRepository, *mocks.MockEmailProviderService, *domain.Workspace) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	ses := mocks.NewMockEmailProviderService(ctrl)
	ws := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{{
		ID: "i1", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{
			Kind:    domain.EmailProviderKindSES,
			Senders: []domain.EmailSender{{ID: "s", Email: "a@b.co", Name: "A"}},
			SES:     &domain.AmazonSESSettings{Region: "us-east-1", AccessKey: "k", SecretKey: "s"},
		},
	}}}
	return &EmailService{workspaceRepo: repo, sesService: ses, secretKey: "test-secret-key"}, repo, ses, ws
}

func TestVeridianAdminVerifyEmailIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("transport ok : verifie vers un 'to' libre", func(t *testing.T) {
		s, repo, ses, ws := verifyFixture(t)
		repo.EXPECT().GetByID(gomock.Any(), "ws").Return(ws, nil)
		ses.EXPECT().SendEmail(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, r domain.SendEmailProviderRequest) error {
			assert.Equal(t, "anyone@example.org", r.To)
			return nil
		})
		repo.EXPECT().Update(gomock.Any(), ws).Return(nil)
		require.NoError(t, s.VeridianAdminVerifyEmailIntegration(ctx, "ws", "i1", "anyone@example.org"))
		require.NotNil(t, ws.Integrations[0].EmailProvider.VeridianTransportVerifiedAt)
	})

	t.Run("transport en echec : NON verifie, aucune ecriture", func(t *testing.T) {
		s, repo, ses, ws := verifyFixture(t)
		repo.EXPECT().GetByID(gomock.Any(), "ws").Return(ws, nil)
		ses.EXPECT().SendEmail(gomock.Any(), gomock.Any()).Return(errors.New("boom"))
		// pas d'Update attendu
		require.Error(t, s.VeridianAdminVerifyEmailIntegration(ctx, "ws", "i1", "anyone@example.org"))
		assert.Nil(t, ws.Integrations[0].EmailProvider.VeridianTransportVerifiedAt)
	})

	t.Run("entrees invalides ou integration inconnue", func(t *testing.T) {
		s, repo, _, ws := verifyFixture(t)
		var v domain.ValidationError
		assert.True(t, errors.As(s.VeridianAdminVerifyEmailIntegration(ctx, "ws", "i1", "pas-un-mail"), &v))
		assert.True(t, errors.As(s.VeridianAdminVerifyEmailIntegration(ctx, "", "i1", "a@b.co"), &v))
		repo.EXPECT().GetByID(gomock.Any(), "ws").Return(ws, nil)
		assert.True(t, errors.As(s.VeridianAdminVerifyEmailIntegration(ctx, "ws", "nope", "a@b.co"), &v))
	})
}
