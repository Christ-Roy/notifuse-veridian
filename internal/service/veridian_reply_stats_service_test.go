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

func newReplyStatsTestService(t *testing.T) (
	*gomock.Controller,
	domain.VeridianReplyStatsService,
	*mocks.MockVeridianContactReplyRepository,
	*mocks.MockAuthService,
) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockVeridianContactReplyRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := NewVeridianReplyStatsService(repo, authSvc, log)
	return ctrl, svc, repo, authSvc
}

func replyStatsReadWorkspace() *domain.UserWorkspace {
	return &domain.UserWorkspace{
		Role: "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceContacts: {Read: true, Write: true},
		},
	}
}

func TestNewVeridianReplyStatsService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianContactReplyRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)

	svc := NewVeridianReplyStatsService(repo, authSvc, log)
	require.NotNil(t, svc)

	// Le service câblé applique le gardien d'auth : un appel passe bien par
	// AuthenticateUserForWorkspace (preuve que la DP authService est branchée).
	authSvc.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws-wire").
		Return(context.Background(), &domain.User{}, replyStatsReadWorkspace(), nil)
	repo.EXPECT().CountRepliedSince(gomock.Any(), "ws-wire", gomock.Any(), gomock.Any()).Return(0, nil)

	got, err := svc.GetReplyStats(context.Background(), &domain.VeridianReplyStatsRequest{WorkspaceID: "ws-wire"})
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestGetReplyStats(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "ws123"
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)

	t.Run("happy path -> replied count, window forwarded to repo", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newReplyStatsTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, replyStatsReadWorkspace(), nil)
		repo.EXPECT().CountRepliedSince(ctx, workspaceID, since, until).Return(12, nil)

		got, err := svc.GetReplyStats(ctx, &domain.VeridianReplyStatsRequest{
			WorkspaceID: workspaceID,
			Since:       since,
			Until:       until,
		})
		require.NoError(t, err)
		assert.Equal(t, 12, got.Replied)
	})

	t.Run("no window -> zero bounds forwarded (whole history)", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newReplyStatsTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, replyStatsReadWorkspace(), nil)
		repo.EXPECT().CountRepliedSince(ctx, workspaceID, time.Time{}, time.Time{}).Return(5, nil)

		got, err := svc.GetReplyStats(ctx, &domain.VeridianReplyStatsRequest{WorkspaceID: workspaceID})
		require.NoError(t, err)
		assert.Equal(t, 5, got.Replied)
	})

	t.Run("missing workspace_id -> error, no auth call", func(t *testing.T) {
		ctrl, svc, _, _ := newReplyStatsTestService(t)
		defer ctrl.Finish()

		got, err := svc.GetReplyStats(ctx, &domain.VeridianReplyStatsRequest{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "workspace_id is required")
	})

	t.Run("nil request -> error", func(t *testing.T) {
		ctrl, svc, _, _ := newReplyStatsTestService(t)
		defer ctrl.Finish()

		got, err := svc.GetReplyStats(ctx, nil)
		assert.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("auth failure -> wrapped error, no repo call", func(t *testing.T) {
		ctrl, svc, _, authSvc := newReplyStatsTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, nil, nil, errors.New("not a member"))

		got, err := svc.GetReplyStats(ctx, &domain.VeridianReplyStatsRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to authenticate user")
	})

	t.Run("no contacts:read permission -> permission error, no repo call", func(t *testing.T) {
		ctrl, svc, _, authSvc := newReplyStatsTestService(t)
		defer ctrl.Finish()

		noPerms := &domain.UserWorkspace{
			Role: "member",
			Permissions: domain.UserPermissions{
				domain.PermissionResourceContacts: {Read: false, Write: false},
			},
		}
		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, noPerms, nil)

		got, err := svc.GetReplyStats(ctx, &domain.VeridianReplyStatsRequest{WorkspaceID: workspaceID})
		assert.Error(t, err)
		assert.Nil(t, got)
		var permErr *domain.PermissionError
		assert.ErrorAs(t, err, &permErr)
	})

	t.Run("repo error -> wrapped error", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newReplyStatsTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, replyStatsReadWorkspace(), nil)
		repo.EXPECT().CountRepliedSince(ctx, workspaceID, since, until).
			Return(0, errors.New("db down"))

		got, err := svc.GetReplyStats(ctx, &domain.VeridianReplyStatsRequest{
			WorkspaceID: workspaceID,
			Since:       since,
			Until:       until,
		})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to fetch reply stats")
	})
}
