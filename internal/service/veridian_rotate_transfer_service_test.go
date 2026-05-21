package service

// === Veridian patch — Lot K (2026-05-21) ===
// Tests des methodes RotateAPIKey + TransferOwner + RunAPIKeyGraceCleanupOnce
// + ConfigureAPIKeyGraceSupport.

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newVeridianServiceWithGrace etend newVeridianService avec un mock grace repo
// deja injecte. Pour tester le path "grace repo absent", appeler newVeridianService
// directement (apiKeyGraceRepo = nil).
func newVeridianServiceWithGrace(t *testing.T) (*veridianService, *veridianServiceMocks, *mocks.MockVeridianAPIKeyGraceRepository) {
	t.Helper()
	svc, m := newVeridianService(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	graceRepo := mocks.NewMockVeridianAPIKeyGraceRepository(ctrl)
	svc.apiKeyGraceRepo = graceRepo
	return svc, m, graceRepo
}

// === ConfigureAPIKeyGraceSupport ===

func TestConfigureAPIKeyGraceSupport_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	graceRepo := mocks.NewMockVeridianAPIKeyGraceRepository(ctrl)

	svc := NewVeridianService(nil, nil, nil, nil, nil, nil, "", "root@x", "http://x", "secret", logger.NewLogger())
	err := ConfigureAPIKeyGraceSupport(svc, graceRepo)
	require.NoError(t, err)
	impl := svc.(*veridianService)
	assert.Same(t, graceRepo, impl.apiKeyGraceRepo)
}

func TestConfigureAPIKeyGraceSupport_WrongType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	graceRepo := mocks.NewMockVeridianAPIKeyGraceRepository(ctrl)
	// Pass un mock VeridianService (pas *veridianService) → erreur.
	mockSvc := mocks.NewMockVeridianService(ctrl)
	err := ConfigureAPIKeyGraceSupport(mockSvc, graceRepo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a *veridianService")
}

// === RotateAPIKey ===

func TestRotateAPIKey_MissingTenantID(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.RotateAPIKey(context.Background(), domain.RotateAPIKeyInput{Reason: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id required")
}

func TestRotateAPIKey_MissingReason(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.RotateAPIKey(context.Background(), domain.RotateAPIKeyInput{TenantID: "ws-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason required")
}

func TestRotateAPIKey_GraceRepoNotConfigured(t *testing.T) {
	svc, _ := newVeridianService(t)
	// apiKeyGraceRepo NIL → ErrAPIKeyGraceRepoNotConfigured.
	_, err := svc.RotateAPIKey(context.Background(), domain.RotateAPIKeyInput{
		TenantID: "ws-1",
		Reason:   "test",
	})
	require.ErrorIs(t, err, ErrAPIKeyGraceRepoNotConfigured)
}

func TestRotateAPIKey_WorkspaceNotFound(t *testing.T) {
	svc, m, _ := newVeridianServiceWithGrace(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-x").Return(nil, sql.ErrNoRows).Times(1)

	_, err := svc.RotateAPIKey(ctx, domain.RotateAPIKeyInput{
		TenantID: "ws-x",
		Reason:   "test",
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestRotateAPIKey_OK_WithExistingAPIKey(t *testing.T) {
	svc, m, graceRepo := newVeridianServiceWithGrace(t)
	ctx := context.Background()

	owner := &domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{
			UserWorkspace: domain.UserWorkspace{
				UserID:    owner.ID,
				Role:      "owner",
				CreatedAt: time.Now().Add(-1 * time.Hour),
			},
			Email: owner.Email,
			Type:  domain.UserTypeUser,
		},
		{
			UserWorkspace: domain.UserWorkspace{
				UserID:    "old-api-key-uuid",
				Role:      "member",
				CreatedAt: time.Now().Add(-30 * time.Minute),
			},
			Email: "veridian-api-ws-1@notifuse.app.veridian.site",
			Type:  domain.UserTypeAPIKey,
		},
	}, nil).Times(1)

	// ctxAsUser pour caller = owner.
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	// CreateAPIKey appele avec un prefix contenant "veridian-api-ws-1-r".
	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, wsID, prefix string) (string, string, error) {
			assert.Contains(t, prefix, "veridian-api-ws-1-r")
			return "new.jwt.token", prefix + "@notifuse.app.veridian.site", nil
		}).Times(1)

	m.userRepo.EXPECT().MarkVeridianManaged(ctx, gomock.Any()).Return(nil).Times(1)

	// Insert grace entry pour l'ancienne key.
	graceRepo.EXPECT().Insert(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, e *domain.APIKeyGraceEntry) error {
			assert.Equal(t, "old-api-key-uuid", e.APIKeyUserID)
			assert.Equal(t, "ws-1", e.WorkspaceID)
			assert.Equal(t, "test rotation", e.Reason)
			// revoke_at doit etre ~5min dans le futur.
			diff := time.Until(e.RevokeAt)
			assert.True(t, diff > 4*time.Minute && diff < 6*time.Minute,
				"revoke_at doit etre ~5min, got %v", diff)
			return nil
		}).Times(1)

	// Emit event.
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantAPIKeyRotated, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.RotateAPIKey(ctx, domain.RotateAPIKeyInput{
		TenantID: "ws-1",
		Reason:   "test rotation",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "new.jwt.token", resp.NewAPIKey)
	assert.Contains(t, resp.NewAPIKeyEmail, "veridian-api-ws-1-r")
	// revoke_at ~5min future.
	diff := time.Until(resp.OldAPIKeyRevokesAt)
	assert.True(t, diff > 4*time.Minute && diff < 6*time.Minute)
}

func TestRotateAPIKey_OK_NoExistingAPIKey(t *testing.T) {
	// Cas tenant orphelin : aucune api_key actuelle (Provision n'a jamais
	// tourne). On cree quand meme la nouvelle, sans insert grace.
	svc, m, graceRepo := newVeridianServiceWithGrace(t)
	ctx := context.Background()

	owner := &domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{
			UserWorkspace: domain.UserWorkspace{
				UserID: owner.ID,
				Role:   "owner",
			},
			Email: owner.Email,
			Type:  domain.UserTypeUser,
		},
	}, nil).Times(1)

	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-1", gomock.Any()).
		Return("new.jwt", "veridian-api-ws-1-r0@notifuse.app.veridian.site", nil).Times(1)

	m.userRepo.EXPECT().MarkVeridianManaged(ctx, gomock.Any()).Return(nil).Times(1)

	// Pas d'Insert grace puisque pas d'ancienne key.
	graceRepo.EXPECT().Insert(gomock.Any(), gomock.Any()).Times(0)

	m.emitter.EXPECT().Emit(ctx, domain.EventTenantAPIKeyRotated, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.RotateAPIKey(ctx, domain.RotateAPIKeyInput{
		TenantID: "ws-1",
		Reason:   "first key",
	})
	require.NoError(t, err)
	assert.Equal(t, "new.jwt", resp.NewAPIKey)
}

func TestRotateAPIKey_OwnerNotFoundFallbackToRoot(t *testing.T) {
	svc, m, graceRepo := newVeridianServiceWithGrace(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-uuid", Email: "root@veridian.site"}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-orphan").Return(&domain.Workspace{ID: "ws-orphan"}, nil).Times(1)
	// Aucun owner humain — workspace orphelin.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-orphan").Return(nil, nil).Times(1)
	// Fallback : on demande root par email.
	m.userRepo.EXPECT().GetUserByEmail(ctx, "root@veridian.site").Return(rootUser, nil).Times(1)

	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-orphan", gomock.Any()).
		Return("new.jwt", "veridian-api-ws-orphan-r0@x.test", nil).Times(1)
	m.userRepo.EXPECT().MarkVeridianManaged(ctx, gomock.Any()).Return(nil).Times(1)
	graceRepo.EXPECT().Insert(gomock.Any(), gomock.Any()).Times(0)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantAPIKeyRotated, "ws-orphan", gomock.Any()).Times(1)

	_, err := svc.RotateAPIKey(ctx, domain.RotateAPIKeyInput{TenantID: "ws-orphan", Reason: "x"})
	require.NoError(t, err)
}

func TestRotateAPIKey_CreateAPIKeyError(t *testing.T) {
	svc, m, _ := newVeridianServiceWithGrace(t)
	ctx := context.Background()

	owner := &domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: owner.ID, Role: "owner"}, Email: owner.Email, Type: domain.UserTypeUser},
	}, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-1", gomock.Any()).
		Return("", "", errors.New("this user already exists")).Times(1)

	_, err := svc.RotateAPIKey(ctx, domain.RotateAPIKeyInput{TenantID: "ws-1", Reason: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create new api key")
}

func TestRotateAPIKey_GraceInsertError(t *testing.T) {
	svc, m, graceRepo := newVeridianServiceWithGrace(t)
	ctx := context.Background()

	owner := &domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: owner.ID, Role: "owner"}, Email: owner.Email, Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "old-api", Role: "member"}, Type: domain.UserTypeAPIKey},
	}, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-1", gomock.Any()).
		Return("new.jwt", "veridian-api-ws-1-r0@x.test", nil).Times(1)
	m.userRepo.EXPECT().MarkVeridianManaged(ctx, gomock.Any()).Return(nil).Times(1)

	graceRepo.EXPECT().Insert(ctx, gomock.Any()).Return(errors.New("conn dead")).Times(1)

	_, err := svc.RotateAPIKey(ctx, domain.RotateAPIKeyInput{TenantID: "ws-1", Reason: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert grace entry")
}

// === TransferOwner ===

func TestTransferOwner_MissingTenantID(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.TransferOwner(context.Background(), domain.TransferOwnerInput{
		NewOwnerEmail: "n@x.t", Reason: "r",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id required")
}

func TestTransferOwner_MissingNewOwnerEmail(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.TransferOwner(context.Background(), domain.TransferOwnerInput{
		TenantID: "ws-1", Reason: "r",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new_owner_email required")
}

func TestTransferOwner_MissingReason(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.TransferOwner(context.Background(), domain.TransferOwnerInput{
		TenantID: "ws-1", NewOwnerEmail: "n@x.t",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reason required")
}

func TestTransferOwner_TenantNotFound(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// GetWorkspaceUsersWithEmail pour resolver l'old owner — peut echouer
	// silencieusement (best-effort).
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-x").Return(nil, errors.New("ignored")).Times(1)
	// AttachOwner appele ensuite et plante sur GetByID.
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-x").Return(nil, sql.ErrNoRows).Times(1)

	_, err := svc.TransferOwner(ctx, domain.TransferOwnerInput{
		TenantID:      "ws-x",
		NewOwnerEmail: "new@x.test",
		Reason:        "test",
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestTransferOwner_OK_ResolvesOldOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	oldOwner := &domain.User{ID: "old-uuid", Email: "old@x.test", Type: domain.UserTypeUser}
	newOwner := &domain.User{ID: "new-uuid", Email: "new@x.test", Type: domain.UserTypeUser}

	// 1ere call: resolve old owner (pre-AttachOwner).
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: oldOwner.ID, Role: "owner"}, Email: oldOwner.Email, Type: domain.UserTypeUser},
	}, nil).Times(1)

	// AttachOwner path : GetByID OK, GetUserByEmail OK (new owner), GetUserWorkspace
	// returns ErrNoRows (pas attache), GetWorkspaceUsersWithEmail (2eme appel
	// pour resolver current owner) → old owner.
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "new@x.test").Return(newOwner, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, newOwner.ID, "ws-1").Return(nil, sql.ErrNoRows).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: oldOwner.ID, Role: "owner"}, Email: oldOwner.Email, Type: domain.UserTypeUser},
	}, nil).Times(1)

	// ctxAsUser oldOwner (current owner).
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	// AddUserToWorkspace (new owner devient member) + TransferOwnership.
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", newOwner.ID, "member", gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-1", newOwner.ID, oldOwner.ID).Return(nil).Times(1)

	// Root retire (parite Provision) — best-effort. root pas resolu ici, on skip.
	m.userRepo.EXPECT().GetUserByEmail(ctx, "root@veridian.site").Return(nil, errors.New("not root")).Times(1)

	// Emit tenant.owner_changed.
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantOwnerChanged, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.TransferOwner(ctx, domain.TransferOwnerInput{
		TenantID:      "ws-1",
		NewOwnerEmail: "new@x.test",
		Reason:        "test",
	})
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "old@x.test", resp.OldOwner)
	assert.Equal(t, "new@x.test", resp.NewOwner)
	assert.False(t, resp.TransferredAt.IsZero())
}

// === RunAPIKeyGraceCleanupOnce ===

func TestRunAPIKeyGraceCleanupOnce_NoGraceRepo_Noop(t *testing.T) {
	svc, _ := newVeridianService(t) // apiKeyGraceRepo nil
	revoked, errs := svc.RunAPIKeyGraceCleanupOnce(context.Background())
	assert.Equal(t, 0, revoked)
	assert.Empty(t, errs)
}

func TestRunAPIKeyGraceCleanupOnce_ListExpiredError(t *testing.T) {
	svc, _, graceRepo := newVeridianServiceWithGrace(t)
	graceRepo.EXPECT().ListExpired(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")).Times(1)

	revoked, errs := svc.RunAPIKeyGraceCleanupOnce(context.Background())
	assert.Equal(t, 0, revoked)
	assert.Contains(t, errs["__list__"], "boom")
}

func TestRunAPIKeyGraceCleanupOnce_DeletesExpired(t *testing.T) {
	svc, m, graceRepo := newVeridianServiceWithGrace(t)
	ctx := context.Background()
	now := time.Now().UTC()

	entries := []*domain.APIKeyGraceEntry{
		{APIKeyUserID: "u-1", WorkspaceID: "ws-1", RevokeAt: now.Add(-1 * time.Minute)},
		{APIKeyUserID: "u-2", WorkspaceID: "ws-2", RevokeAt: now.Add(-2 * time.Minute)},
	}

	graceRepo.EXPECT().ListExpired(ctx, gomock.Any()).Return(entries, nil).Times(1)
	// deleteAPIKeyUser fait un 2eme call ListExpired pour resolver workspace_id.
	// On le tolere (AnyTimes).
	graceRepo.EXPECT().ListExpired(ctx, gomock.Any()).Return(entries, nil).AnyTimes()

	// RemoveUserFromWorkspace pour chaque entry.
	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(ctx, "u-1", "ws-1").Return(nil).Times(1)
	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(ctx, "u-2", "ws-2").Return(nil).Times(1)

	// DeleteByID pour chaque entry.
	graceRepo.EXPECT().DeleteByID(ctx, "u-1").Return(nil).Times(1)
	graceRepo.EXPECT().DeleteByID(ctx, "u-2").Return(nil).Times(1)

	revoked, errs := svc.RunAPIKeyGraceCleanupOnce(ctx)
	assert.Equal(t, 2, revoked)
	assert.Empty(t, errs)
}

func TestRunAPIKeyGraceCleanupOnce_ContinuesOnDeleteError(t *testing.T) {
	svc, m, graceRepo := newVeridianServiceWithGrace(t)
	ctx := context.Background()
	now := time.Now().UTC()

	entries := []*domain.APIKeyGraceEntry{
		{APIKeyUserID: "u-good", WorkspaceID: "ws-1", RevokeAt: now.Add(-1 * time.Minute)},
		{APIKeyUserID: "u-bad", WorkspaceID: "ws-2", RevokeAt: now.Add(-2 * time.Minute)},
	}

	graceRepo.EXPECT().ListExpired(gomock.Any(), gomock.Any()).Return(entries, nil).AnyTimes()

	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(ctx, "u-good", "ws-1").Return(nil).Times(1)
	graceRepo.EXPECT().DeleteByID(ctx, "u-good").Return(nil).Times(1)

	// u-bad : DeleteByID echoue → erreur dans errs map mais revoked++ pas.
	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(ctx, "u-bad", "ws-2").Return(nil).Times(1)
	graceRepo.EXPECT().DeleteByID(ctx, "u-bad").Return(errors.New("conn dead")).Times(1)

	revoked, errs := svc.RunAPIKeyGraceCleanupOnce(ctx)
	assert.Equal(t, 1, revoked)
	assert.Contains(t, errs["u-bad"], "conn dead")
}
