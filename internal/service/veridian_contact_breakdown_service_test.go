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

func newBreakdownTestService(t *testing.T) (
	*gomock.Controller,
	domain.VeridianContactProviderBreakdownService,
	*mocks.MockVeridianContactProviderBreakdownRepository,
	*mocks.MockAuthService,
) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockVeridianContactProviderBreakdownRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := NewVeridianContactBreakdownService(repo, authSvc, log)
	return ctrl, svc, repo, authSvc
}

func contactsReadWorkspace() *domain.UserWorkspace {
	return &domain.UserWorkspace{
		Role: "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceContacts: {Read: true, Write: true},
		},
	}
}

func TestNewVeridianContactBreakdownService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianContactProviderBreakdownRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	svc := NewVeridianContactBreakdownService(repo, authSvc, log)
	require.NotNil(t, svc)

	// Le service câblé doit appliquer le gardien d'auth : un appel passe bien
	// par AuthenticateUserForWorkspace (preuve que la DP authService est branchée).
	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws-wire").
		Return(context.Background(), &domain.User{}, contactsReadWorkspace(), nil)
	repo.EXPECT().GetProviderClassRows(gomock.Any(), "ws-wire", "").Return(nil, nil)

	got, err := svc.GetProviderBreakdown(context.Background(), &domain.VeridianProviderBreakdownRequest{WorkspaceID: "ws-wire"})
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestGetProviderBreakdown(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "ws123"

	t.Run("happy path -> aggregated breakdown", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newBreakdownTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, contactsReadWorkspace(), nil)

		repo.EXPECT().GetProviderClassRows(ctx, workspaceID, "").Return([]domain.VeridianContactProviderRow{
			{Email: "a@gmail.com"},
			{Email: "b@outlook.com"},
			{Email: "c@acme.io"},
		}, nil)

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{WorkspaceID: workspaceID})
		require.NoError(t, err)
		assert.Equal(t, 3, got.Total)
		assert.Equal(t, 1, got.Breakdown[domain.ProviderClassGoogle])
		assert.Equal(t, 1, got.Breakdown[domain.ProviderClassMicrosoft])
		assert.Equal(t, 1, got.Breakdown[domain.ProviderClassCorporate])
	})

	t.Run("list_id forwarded to repo", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newBreakdownTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, contactsReadWorkspace(), nil)
		repo.EXPECT().GetProviderClassRows(ctx, workspaceID, "list-xyz").
			Return([]domain.VeridianContactProviderRow{{Email: "x@yahoo.fr"}}, nil)

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{
			WorkspaceID: workspaceID,
			ListID:      "list-xyz",
		})
		require.NoError(t, err)
		assert.Equal(t, 1, got.Total)
		assert.Equal(t, 1, got.Breakdown[domain.ProviderClassYahooAol])
	})

	t.Run("empty workspace -> zeroed breakdown", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newBreakdownTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, contactsReadWorkspace(), nil)
		repo.EXPECT().GetProviderClassRows(ctx, workspaceID, "").Return(nil, nil)

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{WorkspaceID: workspaceID})
		require.NoError(t, err)
		assert.Equal(t, 0, got.Total)
		assert.Equal(t, 0, got.Breakdown[domain.ProviderClassGoogle])
	})

	t.Run("missing workspace_id -> error, no auth call", func(t *testing.T) {
		ctrl, svc, _, _ := newBreakdownTestService(t)
		defer ctrl.Finish()

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "workspace_id is required")
	})

	t.Run("nil request -> error", func(t *testing.T) {
		ctrl, svc, _, _ := newBreakdownTestService(t)
		defer ctrl.Finish()

		got, err := svc.GetProviderBreakdown(ctx, nil)
		assert.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("auth failure -> wrapped error, no repo call", func(t *testing.T) {
		ctrl, svc, _, authSvc := newBreakdownTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, nil, nil, errors.New("not a member"))

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to authenticate user")
	})

	t.Run("no contacts:read permission -> permission error, no repo call", func(t *testing.T) {
		ctrl, svc, _, authSvc := newBreakdownTestService(t)
		defer ctrl.Finish()

		noPerms := &domain.UserWorkspace{
			Role: "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceContacts: {Read: false, Write: false},
			},
		}
		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, noPerms, nil)

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		var permErr *domain.PermissionError
		assert.ErrorAs(t, err, &permErr)
	})

	t.Run("repo error -> wrapped error", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newBreakdownTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, contactsReadWorkspace(), nil)
		repo.EXPECT().GetProviderClassRows(ctx, workspaceID, "").
			Return(nil, errors.New("db down"))

		got, err := svc.GetProviderBreakdown(ctx, &domain.VeridianProviderBreakdownRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to fetch provider breakdown")
	})
}
