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

func newEnrollTestService(t *testing.T) (
	*gomock.Controller,
	*VeridianAutomationEnrollService,
	*mocks.MockAutomationRepository,
	*mocks.MockAuthService,
) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockAutomationRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	svc := NewVeridianAutomationEnrollService(repo, authSvc, log)
	return ctrl, svc, repo, authSvc
}

func automationsWriteWorkspace() *domain.UserWorkspace {
	return &domain.UserWorkspace{
		Role: "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceAutomations: {Read: true, Write: true},
		},
	}
}

func liveAutomation() *domain.Automation {
	return &domain.Automation{
		ID:         "auto1",
		Status:     domain.AutomationStatusLive,
		RootNodeID: "root1",
		Trigger:    &domain.TimelineTriggerConfig{EventKind: "contact.created", Frequency: domain.TriggerFrequencyEveryTime},
	}
}

func TestVeridianAutomationEnrollService_Enroll(t *testing.T) {
	ctx := context.Background()
	const workspaceID = "ws1"
	const automationID = "auto1"

	t.Run("happy path enrolls new contact", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(liveAutomation(), nil)
		// contact not yet in automation
		repo.EXPECT().GetContactAutomationByEmail(ctx, workspaceID, automationID, "new@example.com").
			Return(nil, errors.New("contact automation not found"))
		repo.EXPECT().EnrollContact(ctx, workspaceID, automationID, "root1", "new@example.com", domain.TriggerFrequencyEveryTime).
			Return(nil)

		resp, err := svc.Enroll(ctx, workspaceID, automationID, []string{"new@example.com"})
		require.NoError(t, err)
		assert.Equal(t, 1, resp.Enrolled)
		assert.Equal(t, 0, resp.Skipped)
		assert.Equal(t, 0, resp.Failed)
		require.Len(t, resp.Results, 1)
		assert.Equal(t, "enrolled", resp.Results[0].Status)
	})

	t.Run("idempotent: already active contact is skipped (no EnrollContact call)", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(liveAutomation(), nil)
		repo.EXPECT().GetContactAutomationByEmail(ctx, workspaceID, automationID, "active@example.com").
			Return(&domain.ContactAutomation{Status: domain.ContactAutomationStatusActive}, nil)
		// EnrollContact must NOT be called → no EXPECT for it

		resp, err := svc.Enroll(ctx, workspaceID, automationID, []string{"active@example.com"})
		require.NoError(t, err)
		assert.Equal(t, 0, resp.Enrolled)
		assert.Equal(t, 1, resp.Skipped)
		assert.Equal(t, "already_active", resp.Results[0].Status)
	})

	t.Run("completed contact is re-enrolled (not skipped)", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(liveAutomation(), nil)
		repo.EXPECT().GetContactAutomationByEmail(ctx, workspaceID, automationID, "done@example.com").
			Return(&domain.ContactAutomation{Status: domain.ContactAutomationStatusCompleted}, nil)
		repo.EXPECT().EnrollContact(ctx, workspaceID, automationID, "root1", "done@example.com", domain.TriggerFrequencyEveryTime).
			Return(nil)

		resp, err := svc.Enroll(ctx, workspaceID, automationID, []string{"done@example.com"})
		require.NoError(t, err)
		assert.Equal(t, 1, resp.Enrolled)
	})

	t.Run("best-effort: one EnrollContact error does not abort the batch", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(liveAutomation(), nil)

		repo.EXPECT().GetContactAutomationByEmail(ctx, workspaceID, automationID, "ok@example.com").
			Return(nil, errors.New("not found"))
		repo.EXPECT().EnrollContact(ctx, workspaceID, automationID, "root1", "ok@example.com", gomock.Any()).Return(nil)

		repo.EXPECT().GetContactAutomationByEmail(ctx, workspaceID, automationID, "bad@example.com").
			Return(nil, errors.New("not found"))
		repo.EXPECT().EnrollContact(ctx, workspaceID, automationID, "root1", "bad@example.com", gomock.Any()).
			Return(errors.New("db down"))

		resp, err := svc.Enroll(ctx, workspaceID, automationID, []string{"ok@example.com", "bad@example.com"})
		require.NoError(t, err)
		assert.Equal(t, 1, resp.Enrolled)
		assert.Equal(t, 1, resp.Failed)
		require.Len(t, resp.Results, 2)
		assert.Equal(t, "error", resp.Results[1].Status)
		assert.Contains(t, resp.Results[1].Error, "db down")
	})

	t.Run("permission denied returns PermissionError (no repo access)", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()
		_ = repo // no calls expected

		readOnly := &domain.UserWorkspace{
			Role:        "member",
			Permissions: domain.UserPermissions{domain.PermissionResourceAutomations: {Read: true, Write: false}},
		}
		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, readOnly, nil)

		_, err := svc.Enroll(ctx, workspaceID, automationID, []string{"x@example.com"})
		require.Error(t, err)
		var permErr *domain.PermissionError
		assert.True(t, errors.As(err, &permErr))
	})

	t.Run("auth failure propagates with prefix for 401 mapping", func(t *testing.T) {
		ctrl, svc, _, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, nil, nil, errors.New("not a member"))

		_, err := svc.Enroll(ctx, workspaceID, automationID, []string{"x@example.com"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to authenticate user")
	})

	t.Run("non-live automation rejected", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		draft := liveAutomation()
		draft.Status = domain.AutomationStatusDraft
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(draft, nil)

		_, err := svc.Enroll(ctx, workspaceID, automationID, []string{"x@example.com"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not live")
	})

	t.Run("automation without root node rejected", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		noRoot := liveAutomation()
		noRoot.RootNodeID = ""
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(noRoot, nil)

		_, err := svc.Enroll(ctx, workspaceID, automationID, []string{"x@example.com"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "root node")
	})

	t.Run("frequency 'once' from trigger is forwarded to EnrollContact", func(t *testing.T) {
		ctrl, svc, repo, authSvc := newEnrollTestService(t)
		defer ctrl.Finish()

		authSvc.EXPECT().AuthenticateUserForWorkspace(ctx, workspaceID).
			Return(ctx, &domain.User{}, automationsWriteWorkspace(), nil)
		onceAuto := liveAutomation()
		onceAuto.Trigger.Frequency = domain.TriggerFrequencyOnce
		repo.EXPECT().GetByID(ctx, workspaceID, automationID).Return(onceAuto, nil)
		repo.EXPECT().GetContactAutomationByEmail(ctx, workspaceID, automationID, "x@example.com").
			Return(nil, errors.New("not found"))
		repo.EXPECT().EnrollContact(ctx, workspaceID, automationID, "root1", "x@example.com", domain.TriggerFrequencyOnce).
			Return(nil)

		_, err := svc.Enroll(ctx, workspaceID, automationID, []string{"x@example.com"})
		require.NoError(t, err)
	})
}

func TestNewVeridianAutomationEnrollService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockAutomationRepository(ctrl)
	authSvc := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	svc := NewVeridianAutomationEnrollService(repo, authSvc, log)
	require.NotNil(t, svc)
}
