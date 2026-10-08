package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wakeRecorder struct{ woken []string }

func (w *wakeRecorder) WakePendingByIntegration(_ context.Context, _ string, id string) (int64, error) {
	w.woken = append(w.woken, id)
	return 0, nil
}

func adminTestWorkspace() *domain.Workspace {
	mk := func(id string) domain.Integration {
		return domain.Integration{ID: id, Name: id, Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: "smtp.example", Port: 587},
			Senders: []domain.EmailSender{{ID: "s" + id, Email: id + "@example.fr", IsDefault: true}},
		}}
	}
	a, b, tx := mk("a"), mk("b"), mk("tx")
	now := time.Now()
	a.EmailProvider.VeridianTransportVerifiedAt = &now
	b.EmailProvider.VeridianTransportVerifiedAt = &now
	return &domain.Workspace{ID: "ws1", Integrations: []domain.Integration{a, b, tx},
		Settings: domain.WorkspaceSettings{VeridianMarketingEmailProviderIDs: []string{"a", "b"}, MarketingEmailProviderID: "a"}}
}

func newAdminServiceForTest(t *testing.T) (domain.VeridianEmailProfileAdminService, *mocks.MockWorkspaceRepository, *mocks.MockAuthService, *wakeRecorder) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	auth := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	wake := &wakeRecorder{}
	return NewVeridianEmailProfileAdminService(repo, auth, wake, log), repo, auth, wake
}

func writer() *domain.UserWorkspace {
	return &domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{domain.PermissionResourceWorkspace: {Read: true, Write: true}}}
}

func TestAdminService_SetUsageWritesOnceAndWakesTheProfiles(t *testing.T) {
	svc, repo, auth, wake := newAdminServiceForTest(t)
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, writer(), nil)
	repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(adminTestWorkspace(), nil)
	var saved *domain.Workspace
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w *domain.Workspace) error { saved = w; return nil }).Times(1)

	res, err := svc.SetUsage(context.Background(), domain.VeridianSetUsageRequest{WorkspaceID: "ws1", IntegrationID: "tx", Usage: "transactional"})
	require.NoError(t, err)
	assert.Equal(t, "tx", res.TransactionalIntegrationID)
	assert.Equal(t, []string{"a", "b"}, res.Rotation)
	require.NotNil(t, saved)
	assert.Equal(t, "tx", saved.Settings.TransactionalEmailProviderID)
	assert.Contains(t, wake.woken, "tx")
}

func TestAdminService_RefusesWithoutWritePermissionAndSurfacesRuleErrors(t *testing.T) {
	t.Run("lecture seule", func(t *testing.T) {
		svc, _, auth, _ := newAdminServiceForTest(t)
		readOnly := &domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{domain.PermissionResourceWorkspace: {Read: true}}}
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, readOnly, nil)
		_, err := svc.Pause(context.Background(), domain.VeridianPauseRequest{WorkspaceID: "ws1", IntegrationID: "a"})
		var perm *domain.PermissionError
		require.ErrorAs(t, err, &perm)
	})
	t.Run("authentification", func(t *testing.T) {
		svc, _, auth, _ := newAdminServiceForTest(t)
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), nil, nil, errors.New("nope"))
		_, err := svc.SetUsage(context.Background(), domain.VeridianSetUsageRequest{WorkspaceID: "ws1", IntegrationID: "a", Usage: "commercial"})
		require.Error(t, err)
	})
	t.Run("requete invalide : 400, aucune lecture", func(t *testing.T) {
		svc, _, _, _ := newAdminServiceForTest(t)
		_, err := svc.SetUsage(context.Background(), domain.VeridianSetUsageRequest{WorkspaceID: "ws1", IntegrationID: "a", Usage: "boum"})
		var v domain.ValidationError
		require.ErrorAs(t, err, &v)
	})
	t.Run("profil non verifie refuse, rien n'est ecrit", func(t *testing.T) {
		svc, repo, auth, _ := newAdminServiceForTest(t)
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, writer(), nil)
		repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(adminTestWorkspace(), nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)
		_, err := svc.SetUsage(context.Background(), domain.VeridianSetUsageRequest{WorkspaceID: "ws1", IntegrationID: "tx", Usage: "commercial"})
		var v domain.ValidationError
		require.ErrorAs(t, err, &v)
	})
}

func TestNewVeridianEmailProfileAdminServiceRetainsDependencies(t *testing.T) {
	svc, repo, auth, wake := newAdminServiceForTest(t)
	concrete, ok := svc.(*veridianEmailProfileAdminService)
	require.True(t, ok)
	assert.Same(t, repo, concrete.repo)
	assert.Same(t, auth, concrete.auth)
	assert.Same(t, wake, concrete.policyRepo)
}

func TestAdminService_PauseAndResume(t *testing.T) {
	svc, repo, auth, wake := newAdminServiceForTest(t)
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, writer(), nil).Times(2)
	repo.EXPECT().GetByID(gomock.Any(), "ws1").Return(adminTestWorkspace(), nil).Times(2)
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, w *domain.Workspace) error {
		return nil
	}).Times(2)
	p, err := svc.Pause(context.Background(), domain.VeridianPauseRequest{WorkspaceID: "ws1", IntegrationID: "a"})
	require.NoError(t, err)
	assert.True(t, p.Paused)
	assert.Empty(t, wake.woken, "la pause ne reveille rien")
	r, err := svc.Resume(context.Background(), domain.VeridianPauseRequest{WorkspaceID: "ws1", IntegrationID: "a"})
	require.NoError(t, err)
	assert.False(t, r.Paused)
	assert.Equal(t, []string{"a"}, wake.woken, "la reprise reveille la file du profil")
}
