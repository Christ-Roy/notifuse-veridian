package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func newEngagementTestService(t *testing.T) (
	*gomock.Controller,
	domain.VeridianEngagementByClassService,
	*mocks.MockVeridianEngagementByClassRepository,
	*mocks.MockAuthService,
) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockVeridianEngagementByClassRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := NewVeridianEngagementByClassService(repo, authSvc, log)
	return ctrl, svc, repo, authSvc
}

func TestNewVeridianEngagementByClassService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianEngagementByClassRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	svc := NewVeridianEngagementByClassService(repo, authSvc, log)
	require.NotNil(t, svc)

	// Câblage : un appel passe par AuthenticateUserForWorkspace (DP branchée).
	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws-wire").
		Return(context.Background(), &domain.User{}, contactsReadWorkspace(), nil)
	repo.EXPECT().GetEngagementByDomain(gomock.Any(), "ws-wire", gomock.Any(), gomock.Any()).Return(nil, nil)

	got, err := svc.GetEngagementByClass(context.Background(), &domain.VeridianEngagementByClassRequest{WorkspaceID: "ws-wire"})
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestGetEngagementByClass(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "ws123"
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)

	t.Run("happy path -> aggregated engagement, bounds forwarded", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEngagementTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, contactsReadWorkspace(), nil)
		repo.EXPECT().GetEngagementByDomain(ctx, workspaceID, since, until).
			Return([]domain.VeridianDomainEngagementRow{
				{Domain: "gmail.com", Sent: 10, Delivered: 9, Bounced: 1, Opened: 4, Clicked: 1},
				{Domain: "outlook.com", Sent: 5, Delivered: 5, Bounced: 0, Opened: 2, Clicked: 0},
			}, nil)

		got, err := svc.GetEngagementByClass(ctx, &domain.VeridianEngagementByClassRequest{
			WorkspaceID: workspaceID,
			Since:       since,
			Until:       until,
		})
		require.NoError(t, err)
		assert.Equal(t, 10, got.ByClass[domain.ProviderClassGoogle].Sent)
		assert.Equal(t, 1, got.ByClass[domain.ProviderClassGoogle].Bounced)
		assert.Equal(t, 5, got.ByClass[domain.ProviderClassMicrosoft].Sent)
		assert.Equal(t, 15, got.Total.Sent)
	})

	t.Run("missing workspace_id -> error, no auth call", func(t *testing.T) {
		ctrl, svc, _, _ := newEngagementTestService(t)
		defer ctrl.Finish()

		got, err := svc.GetEngagementByClass(ctx, &domain.VeridianEngagementByClassRequest{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "workspace_id is required")
	})

	t.Run("nil request -> error", func(t *testing.T) {
		ctrl, svc, _, _ := newEngagementTestService(t)
		defer ctrl.Finish()

		got, err := svc.GetEngagementByClass(ctx, nil)
		assert.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("auth failure -> wrapped error, no repo call", func(t *testing.T) {
		ctrl, svc, _, authSvc := newEngagementTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, nil, nil, errors.New("not a member"))

		got, err := svc.GetEngagementByClass(ctx, &domain.VeridianEngagementByClassRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to authenticate user")
	})

	t.Run("no contacts:read permission -> permission error, no repo call", func(t *testing.T) {
		ctrl, svc, _, authSvc := newEngagementTestService(t)
		defer ctrl.Finish()

		noPerms := &domain.UserWorkspace{
			Role: "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceContacts: {Read: false, Write: false},
			},
		}
		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, noPerms, nil)

		got, err := svc.GetEngagementByClass(ctx, &domain.VeridianEngagementByClassRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		var permErr *domain.PermissionError
		assert.ErrorAs(t, err, &permErr)
	})

	t.Run("repo error -> wrapped error", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEngagementTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, contactsReadWorkspace(), nil)
		repo.EXPECT().GetEngagementByDomain(ctx, workspaceID, gomock.Any(), gomock.Any()).
			Return(nil, errors.New("db down"))

		got, err := svc.GetEngagementByClass(ctx, &domain.VeridianEngagementByClassRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to fetch engagement by class")
	})
}
