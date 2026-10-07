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
	assert.False(t, got.AnyStopped)
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

	// nord : ionos ralentie (4 % >= 3 %), ovh libre.
	msgRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).Return(0, nil)
	msgRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).Return(50, nil)
	msgRepo.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).Return(1, nil)
	msgRepo.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws1", "messagerie-nord-776.fr", gomock.Any()).
		Return(map[string]domain.VeridianReputationCounts{"ionos": {Sent: 25, HardBounces: 1}, "ovh": {Sent: 25}}, nil)

	// agence : une plainte (alerte, domaine ralenti, jamais arrete) + ionos qui refuse en bloc.
	msgRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).Return(1, nil)
	msgRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).Return(60, nil)
	msgRepo.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).Return(12, nil)
	msgRepo.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", gomock.Any()).
		Return(map[string]domain.VeridianReputationCounts{"ionos": {Sent: 30, PolicyRefusals: 14}, "ovh": {Sent: 30}}, nil)
	msgRepo.EXPECT().RecentClassOutcomesForSenderDomain(gomock.Any(), "ws1", "agence-veridian.fr", "ionos", 20, gomock.Any()).Return(20, 12, nil)

	got, err := svc.GetReputationStatus(ctx, &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.NoError(t, err)
	require.Len(t, got.Integrations, 2)
	assert.True(t, got.AnyStopped)
	assert.True(t, got.AnySlowed)

	byID := map[string]domain.VeridianReputationIntegrationStatus{}
	for _, i := range got.Integrations {
		byID[i.IntegrationID] = i
	}
	assert.Equal(t, []string{"ionos"}, byID["nord"].SlowedClasses, "nord : ionos ralentie")
	assert.Empty(t, byID["nord"].StoppedClasses)
	assert.Equal(t, 1, byID["nord"].DomainFactor)
	assert.False(t, byID["nord"].Alert)
	require.Len(t, byID["nord"].Classes, 2)

	assert.True(t, byID["agence"].Alert)
	assert.Equal(t, 4, byID["agence"].DomainFactor, "plainte : domaine ralenti x4, pas arrete")
	assert.Equal(t, 20, byID["agence"].MinSent)
	assert.Equal(t, []string{"ionos"}, byID["agence"].StoppedClasses, "seul ionos (refus en bloc) est arrete")
	for _, c := range byID["agence"].Classes {
		if c.Class == "ovh" {
			assert.False(t, c.Stopped)
			assert.Equal(t, 4, c.Factor)
			assert.Equal(t, "complaint", c.Reason)
		}
	}
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

// Seuil par profil (2026-10-07) : un MEME taux de 6% est gele sur un profil au defaut 3%,
// libre sur un profil a 0.08, et l'API expose le seuil effectif et son origine.
func TestGetReputationStatus_PerProfileThreshold(t *testing.T) {
	ctrl, svc, msgRepo, wsRepo, authSvc := newReputationStatusTestService(t)
	defer ctrl.Finish()

	ctx := context.Background()
	authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, "ws1").
		Return(ctx, &domain.User{}, reputationReadWorkspace(), nil)
	wsRepo.EXPECT().GetByID(ctx, "ws1").Return(&domain.Workspace{
		ID: "ws1",
		Integrations: []domain.Integration{
			{ID: "defaut", Name: "profil-defaut", EmailProvider: domain.EmailProvider{
				Kind: domain.EmailProviderKindSMTP, Senders: []domain.EmailSender{{Email: "a@defaut-veridian.fr"}}}},
			{ID: "relache", Name: "profil-8pct", EmailProvider: domain.EmailProvider{
				Kind: domain.EmailProviderKindSMTP, VeridianHardBounceFreezeThreshold: 0.08,
				Senders: []domain.EmailSender{{Email: "a@relache-veridian.fr"}}}},
		},
	}, nil)
	for _, d := range []string{"defaut-veridian.fr", "relache-veridian.fr"} {
		msgRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws1", d, gomock.Any()).Return(0, nil)
		msgRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws1", d, gomock.Any()).Return(100, nil)
		msgRepo.EXPECT().CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws1", d, gomock.Any()).Return(6, nil) // 6%
		msgRepo.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws1", d, gomock.Any()).
			Return(map[string]domain.VeridianReputationCounts{"ionos": {Sent: 100, HardBounces: 6}}, nil)
	}

	got, err := svc.GetReputationStatus(ctx, &domain.VeridianReputationStatusRequest{WorkspaceID: "ws1"})
	require.NoError(t, err)
	require.Len(t, got.Integrations, 2)

	assert.Equal(t, []string{"ionos"}, got.Integrations[0].SlowedClasses, "6%% = 2 x 3%% : ionos ralentie")
	assert.Equal(t, 4, got.Integrations[0].Classes[0].Factor)
	assert.InDelta(t, 0.03, got.Integrations[0].Threshold, 1e-9)
	assert.False(t, got.Integrations[0].ThresholdCustom)

	assert.Empty(t, got.Integrations[1].SlowedClasses, "6%% ne ralentit pas un profil a 8%%")
	assert.Equal(t, 1, got.Integrations[1].Classes[0].Factor)
	assert.InDelta(t, 0.08, got.Integrations[1].Threshold, 1e-9)
	assert.True(t, got.Integrations[1].ThresholdCustom)
	assert.True(t, got.AnySlowed)
	assert.False(t, got.AnyStopped)
}
