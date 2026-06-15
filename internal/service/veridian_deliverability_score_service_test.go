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

func newScoreTestService(t *testing.T) (
	*gomock.Controller,
	domain.VeridianDeliverabilityScoreService,
	*mocks.MockAuthService,
) {
	ctrl := gomock.NewController(t)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := NewVeridianDeliverabilityScoreService(authSvc, log)
	return ctrl, svc, authSvc
}

func templatesReadWorkspace() *domain.UserWorkspace {
	return &domain.UserWorkspace{
		Role: "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTemplates: {Read: true, Write: true},
		},
	}
}

const goodColdBody = `Bonjour Marie,

Je suis tombé sur Cabinet Durand en cherchant des cabinets d'architecture à Lyon.
Votre approche du réemploi de matériaux m'a parlé. Auriez-vous 15 minutes la
semaine prochaine pour en discuter ? Bien à vous, Robert`

func TestNewVeridianDeliverabilityScoreService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	svc := NewVeridianDeliverabilityScoreService(authSvc, log)
	require.NotNil(t, svc)

	// Preuve que le gardien d'auth est branché : un appel passe par
	// AuthenticateUserForWorkspace.
	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws-wire").
		Return(context.Background(), &domain.User{}, templatesReadWorkspace(), nil)

	got, err := svc.Score(context.Background(), &domain.VeridianDeliverabilityScoreRequest{
		WorkspaceID: "ws-wire",
		Subject:     "Question rapide",
		Body:        goodColdBody,
	})
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestVeridianDeliverabilityScore_Score(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "ws123"

	t.Run("clean cold -> low score, not risky", func(t *testing.T) {
		ctrl, svc, authSvc := newScoreTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, templatesReadWorkspace(), nil)

		got, err := svc.Score(ctx, &domain.VeridianDeliverabilityScoreRequest{
			WorkspaceID: workspaceID,
			Subject:     "Question rapide sur Cabinet Durand",
			Body:        goodColdBody,
			IsHTML:      false,
		})
		require.NoError(t, err)
		assert.False(t, got.IsRisky)
		assert.LessOrEqual(t, got.Score, 2.0)
	})

	t.Run("spammy template -> risky score", func(t *testing.T) {
		ctrl, svc, authSvc := newScoreTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, templatesReadWorkspace(), nil)

		got, err := svc.Score(ctx, &domain.VeridianDeliverabilityScoreRequest{
			WorkspaceID: workspaceID,
			Subject:     "RE: FREE $$$ ACT NOW!!!",
			Body:        "<img src=x> CLICK HERE {{ first_name }} {a|b} http://a http://b http://c http://d",
			IsHTML:      true,
			Mode:        "strict",
		})
		require.NoError(t, err)
		assert.True(t, got.IsRisky)
		assert.Equal(t, "strict", got.Mode)
	})

	t.Run("provider_class drives mode", func(t *testing.T) {
		ctrl, svc, authSvc := newScoreTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, templatesReadWorkspace(), nil)

		got, err := svc.Score(ctx, &domain.VeridianDeliverabilityScoreRequest{
			WorkspaceID:   workspaceID,
			Subject:       "Proposition",
			Body:          goodColdBody,
			ProviderClass: "google",
		})
		require.NoError(t, err)
		assert.Equal(t, "strict", got.Mode)
	})

	t.Run("missing workspace_id -> error, no auth call", func(t *testing.T) {
		ctrl, svc, _ := newScoreTestService(t)
		defer ctrl.Finish()

		got, err := svc.Score(ctx, &domain.VeridianDeliverabilityScoreRequest{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "workspace_id is required")
	})

	t.Run("nil request -> error", func(t *testing.T) {
		ctrl, svc, _ := newScoreTestService(t)
		defer ctrl.Finish()

		got, err := svc.Score(ctx, nil)
		assert.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("auth failure -> wrapped error", func(t *testing.T) {
		ctrl, svc, authSvc := newScoreTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, nil, nil, errors.New("not a member"))

		got, err := svc.Score(ctx, &domain.VeridianDeliverabilityScoreRequest{
			WorkspaceID: workspaceID,
			Body:        goodColdBody,
		})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to authenticate user")
	})

	t.Run("no templates:read -> permission error", func(t *testing.T) {
		ctrl, svc, authSvc := newScoreTestService(t)
		defer ctrl.Finish()

		noPerms := &domain.UserWorkspace{
			Role: "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceTemplates: {Read: false, Write: false},
			},
		}
		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, noPerms, nil)

		got, err := svc.Score(ctx, &domain.VeridianDeliverabilityScoreRequest{
			WorkspaceID: workspaceID,
			Body:        goodColdBody,
		})
		assert.Error(t, err)
		assert.Nil(t, got)
		var permErr *domain.PermissionError
		assert.ErrorAs(t, err, &permErr)
	})
}
