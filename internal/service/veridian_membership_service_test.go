package service

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
// Tests unitaires de veridianService.SyncMember, RemoveMember, RestoreMember.
//
// Couvre :
//   - SyncMember : new user, existing user, idempotent same role, owner kept
//                  no-downgrade, tenant not found, validation inputs
//   - RemoveMember : success, owner refused, idempotent missing user,
//                    idempotent not member, tenant not found
//   - RestoreMember : success new user, success existing user, idempotent
//                     already member, tenant not found

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture : owner user humain pour resolveWorkspaceCaller (filtre Type=User).
func membershipOwnerWithEmail(ownerID, ownerEmail string) *domain.UserWorkspaceWithEmail {
	return &domain.UserWorkspaceWithEmail{
		UserWorkspace: domain.UserWorkspace{
			UserID:      ownerID,
			WorkspaceID: "ws-1",
			Role:        "owner",
		},
		Email: ownerEmail,
		Type:  domain.UserTypeUser,
	}
}

// fixture : entree user_workspaces pour les tests.
func membershipEntry(userID, workspaceID, role string) *domain.UserWorkspace {
	return &domain.UserWorkspace{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Role:        role,
	}
}

// fixture : user member type=user.
func membershipMemberUser() *domain.User {
	return &domain.User{
		ID:    "member-uuid-1",
		Email: "alice@example.com",
		Type:  domain.UserTypeUser,
	}
}

// === SyncMember ============================================================

func TestSyncMember_NewUser_CreatedAndAttached(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)

	// User inconnu → CreateUser.
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil)

	// Pas dans user_workspaces.
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws-1").
		Return(nil, sql.ErrNoRows)

	// Resolveur owner.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)

	// ctxAsUser session.
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)

	// AddUserToWorkspace.
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", gomock.Any(), "member", gomock.Any()).Return(nil)

	// Cleanup.
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		HubUserID: "hub-u-1",
		Role:      domain.SyncMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Synced)
	assert.Equal(t, "member", resp.AppRole)
	assert.NotEmpty(t, resp.AppUserID)
}

func TestSyncMember_ExistingUser_AttachedByEmail(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "member-uuid-1", "member", gomock.Any()).Return(nil)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		HubUserID: "hub-u-1",
		Role:      domain.SyncMemberRoleAdmin, // Hub envoie admin, on mappe en member cote Notifuse
	})
	require.NoError(t, err)
	assert.Equal(t, "member", resp.AppRole, "Hub admin → Notifuse member (workspace upstream sans admin)")
}

func TestSyncMember_Idempotent_AlreadyMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").
		Return(membershipEntry("member-uuid-1", "ws-1", "member"), nil)
	// Pas d'AddUserToWorkspace, pas de session — idempotent path.

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		HubUserID: "hub-u-1",
		Role:      domain.SyncMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Synced)
	assert.Equal(t, "member", resp.AppRole)
}

func TestSyncMember_AlreadyOwner_NoDowngrade(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "owner@x.test").
		Return(&domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}, nil)
	// Le user est DEJA owner → service garde owner (additif, jamais downgrade).
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "owner-uuid", "ws-1").
		Return(membershipEntry("owner-uuid", "ws-1", "owner"), nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "owner@x.test",
		HubUserID: "hub-u-1",
		Role:      domain.SyncMemberRoleMember,
	})
	require.NoError(t, err)
	assert.Equal(t, "owner", resp.AppRole, "owner must not be downgraded by sync-member")
}

func TestSyncMember_WorkspaceNotFound_ReturnsErrNoRows(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-unknown",
		UserEmail: "a@x.test",
		HubUserID: "hub-u-1",
		Role:      domain.SyncMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestSyncMember_ValidationErrors(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		input domain.SyncMemberInput
	}{
		{"missing tenant", domain.SyncMemberInput{UserEmail: "a@x", HubUserID: "u", Role: domain.SyncMemberRoleMember}},
		{"missing email", domain.SyncMemberInput{TenantID: "ws-1", HubUserID: "u", Role: domain.SyncMemberRoleMember}},
		{"missing hub_user_id", domain.SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x", Role: domain.SyncMemberRoleMember}},
		{"invalid role", domain.SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x", HubUserID: "u", Role: "superuser"}},
		{"owner role refused", domain.SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x", HubUserID: "u", Role: "owner"}},
	}
	for _, tc := range cases {
		_, err := svc.SyncMember(ctx, tc.input)
		assert.Error(t, err, tc.name)
	}
}

// === RemoveMember ==========================================================

func TestRemoveMember_Success_HardDeletes(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").
		Return(membershipEntry("member-uuid-1", "ws-1", "member"), nil)
	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil)

	resp, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		Reason:    "admin_action",
	})
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.False(t, resp.RemovedAt.IsZero())
}

func TestRemoveMember_Owner_ReturnsErrCannotRemoveOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "owner@x.test").
		Return(&domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "owner-uuid", "ws-1").
		Return(membershipEntry("owner-uuid", "ws-1", "owner"), nil)
	// RemoveUserFromWorkspace NE doit PAS etre appele (refus prealable).

	_, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		UserEmail: "owner@x.test",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCannotRemoveOwner))
}

func TestRemoveMember_Idempotent_UnknownUser(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "ghost@x.test").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})
	// Pas de GetUserWorkspace, pas de RemoveUserFromWorkspace — idempotent return.

	resp, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		UserEmail: "ghost@x.test",
	})
	require.NoError(t, err)
	assert.False(t, resp.RemovedAt.IsZero())
}

func TestRemoveMember_Idempotent_NotMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)

	resp, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
	})
	require.NoError(t, err)
	assert.False(t, resp.RemovedAt.IsZero())
}

func TestRemoveMember_WorkspaceNotFound_ReturnsErrNoRows(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-unknown",
		UserEmail: "a@x.test",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestRemoveMember_ValidationErrors(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		input domain.RemoveMemberInput
	}{
		{"missing tenant", domain.RemoveMemberInput{UserEmail: "a@x"}},
		{"missing email", domain.RemoveMemberInput{TenantID: "ws-1"}},
	}
	for _, tc := range cases {
		_, err := svc.RemoveMember(ctx, tc.input)
		assert.Error(t, err, tc.name)
	}
}

// === RestoreMember =========================================================

func TestRestoreMember_Success_NewUser(t *testing.T) {
	// User supprime entre-temps → CreateUser puis AddUserToWorkspace.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", gomock.Any(), "member", gomock.Any()).Return(nil)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.False(t, resp.RestoredAt.IsZero())
}

func TestRestoreMember_Success_ExistingUser(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "member-uuid-1", "member", gomock.Any()).Return(nil)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	_, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
	})
	require.NoError(t, err)
}

func TestRestoreMember_Idempotent_AlreadyMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").
		Return(membershipEntry("member-uuid-1", "ws-1", "member"), nil)
	// Pas d'AddUserToWorkspace.

	resp, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
	})
	require.NoError(t, err)
	assert.False(t, resp.RestoredAt.IsZero())
}

func TestRestoreMember_WorkspaceNotFound_ReturnsErrNoRows(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-unknown",
		UserEmail: "a@x.test",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestRestoreMember_ValidationErrors(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		input domain.RestoreMemberInput
	}{
		{"missing tenant", domain.RestoreMemberInput{UserEmail: "a@x"}},
		{"missing email", domain.RestoreMemberInput{TenantID: "ws-1"}},
	}
	for _, tc := range cases {
		_, err := svc.RestoreMember(ctx, tc.input)
		assert.Error(t, err, tc.name)
	}
}

// === resolveWorkspaceCaller ===============================================

func TestResolveWorkspaceCaller_PrefersOwnerHuman(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		// API key avant owner — la fonction doit ignorer les non-humains.
		{UserWorkspace: domain.UserWorkspace{UserID: "api-1", WorkspaceID: "ws-1", Role: "member"}, Email: "api@key", Type: domain.UserTypeAPIKey},
		membershipOwnerWithEmail("owner-1", "owner@x.test"),
	}, nil)

	callerID, err := svc.resolveWorkspaceCaller(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "owner-1", callerID)
}

func TestResolveWorkspaceCaller_FallbackRoot(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Aucun owner humain.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return(nil, nil)
	m.userRepo.EXPECT().GetUserByEmail(ctx, "root@veridian.site").Return(&domain.User{ID: "root-uuid", Email: "root@veridian.site"}, nil)

	callerID, err := svc.resolveWorkspaceCaller(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "root-uuid", callerID)
}

func TestResolveWorkspaceCaller_NoOwnerNoRoot_Error(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.rootEmail = "" // disable root fallback
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return(nil, nil)

	_, err := svc.resolveWorkspaceCaller(ctx, "ws-1")
	require.Error(t, err)
}

// === Helpers placeholder ===

var _ = time.Now // garde l'import time si non utilise par les tests
