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
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

// Correctif 2026-09-29 (fusible de réputation) : service de lecture du signal
// "visible dans l'interface ou l'API" — cf. veridian_reputation_gate.go et
// internal/http/veridian_reputation_status_handler.go.

func newReputationStatusTestService(t *testing.T) (
	*gomock.Controller,
	domain.VeridianReputationStatusService,
	*mocks.MockMessageHistoryRepository,
	*mocks.MockWorkspaceRepository,
	*mocks.MockAuthService,
) {
	ctrl := gomock.NewController(t)
	msgRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := NewVeridianReputationStatusService(msgRepo, wsRepo, authSvc, log)
	return ctrl, svc, msgRepo, wsRepo, authSvc
}

func reputationReadWorkspace() *domain.UserWorkspace {
	return &domain.UserWorkspace{
		Role: "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceMessageHistory: {Read: true, Write: true},
		},
	}
}

func TestGetReputationStatus_MissingWorkspaceID(t *testing.T) {
	ctrl, svc, _, _, _ := newReputationStatusTestService(t)
	defer ctrl.Finish()

	_, err := svc.GetReputationStatus(context.Background(), &domain.VeridianReputationStatusRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_id")
}

func TestGetReputationStatus_AuthFailure(t *testing.T) {
	ctrl, svc, _, _, authSvc := newReputationStatusTestService(t)
	defer ctrl.Finish()

	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").
		Return(context.Background(), nil, nil, errors.New("no session"))

	_, err := svc.GetReputationStatus(context.Background(), &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.Error(t, err)
}

func TestGetReputationStatus_InsufficientPermission(t *testing.T) {
	ctrl, svc, _, _, authSvc := newReputationStatusTestService(t)
	defer ctrl.Finish()

	ctx := context.Background()
	authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, "ws1").
		Return(ctx, &domain.User{}, &domain.UserWorkspace{Role: "member"}, nil) // aucune permission

	_, err := svc.GetReputationStatus(ctx, &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.Error(t, err)
	var permErr *domain.PermissionError
	require.ErrorAs(t, err, &permErr)
}

func TestGetReputationStatus_SkipsNonEmailIntegrations(t *testing.T) {
	ctrl, svc, msgRepo, wsRepo, authSvc := newReputationStatusTestService(t)
	defer ctrl.Finish()

	ctx := context.Background()
	authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, "ws1").
		Return(ctx, &domain.User{}, reputationReadWorkspace(), nil)
	wsRepo.EXPECT().GetByID(ctx, "ws1").Return(&domain.Workspace{
		ID: "ws1",
		Integrations: []domain.Integration{
			{ID: "imap-1", EmailProvider: domain.EmailProvider{Kind: ""}}, // pas email
			{ID: "smtp-nosender", EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}}, // pas de sender
		},
	}, nil)

	// Aucun COUNT attendu : ni l'IMAP ni le SMTP sans sender n'ont d'infra
	// attribuable — le mock refuserait tout appel non déclaré.
	got, err := svc.GetReputationStatus(ctx, &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.NoError(t, err)
	assert.Empty(t, got.Integrations)
	assert.False(t, got.AnyFrozen)
	_ = msgRepo
}

func TestGetReputationStatus_HealthyAndFrozenIntegrations(t *testing.T) {
	ctrl, svc, msgRepo, wsRepo, authSvc := newReputationStatusTestService(t)
	defer ctrl.Finish()

	ctx := context.Background()
	authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, "ws1").
		Return(ctx, &domain.User{}, reputationReadWorkspace(), nil)
	wsRepo.EXPECT().GetByID(ctx, "ws1").Return(&domain.Workspace{
		ID: "ws1",
		Integrations: []domain.Integration{
			{
				ID: "nord", Name: "nord-propre-1",
				EmailProvider: domain.EmailProvider{
					Kind:    domain.EmailProviderKindSMTP,
					Senders: []domain.EmailSender{{Email: "robert@messagerie-nord-776.fr"}},
				},
			},
			{
				ID: "agence", Name: "relai-agence-2",
				EmailProvider: domain.EmailProvider{
					Kind:    domain.EmailProviderKindSMTP,
					Senders: []domain.EmailSender{{Email: "r.brunon@agence-veridian.fr"}},
				},
			},
		},
	}, nil)

	// nord : sain.
	msgRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).Return(0, nil)
	msgRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).Return(50, nil)
	msgRepo.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).Return(1, nil) // 2%

	// agence : gelée (bounce dur).
	msgRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).Return(0, nil)
	msgRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).Return(30, nil)
	msgRepo.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).Return(5, nil) // 16.7%

	got, err := svc.GetReputationStatus(ctx, &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.NoError(t, err)
	require.Len(t, got.Integrations, 2)
	assert.True(t, got.AnyFrozen)

	byID := map[string]domain.VeridianReputationIntegrationStatus{}
	for _, i := range got.Integrations {
		byID[i.IntegrationID] = i
	}
	assert.False(t, byID["nord"].Frozen)
	assert.True(t, byID["agence"].Frozen)
	assert.Equal(t, "hard_bounce_rate", byID["agence"].FrozenReason)
}

func TestGetReputationStatus_RepositoryErrorPropagates(t *testing.T) {
	ctrl, svc, msgRepo, wsRepo, authSvc := newReputationStatusTestService(t)
	defer ctrl.Finish()

	ctx := context.Background()
	authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, "ws1").
		Return(ctx, &domain.User{}, reputationReadWorkspace(), nil)
	wsRepo.EXPECT().GetByID(ctx, "ws1").Return(&domain.Workspace{
		ID: "ws1",
		Integrations: []domain.Integration{
			{ID: "nord", EmailProvider: domain.EmailProvider{
				Kind:    domain.EmailProviderKindSMTP,
				Senders: []domain.EmailSender{{Email: "robert@messagerie-nord-776.fr"}},
			}},
		},
	}, nil)
	msgRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).
		Return(0, errors.New("db down"))

	_, err := svc.GetReputationStatus(ctx, &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.Error(t, err)
}
