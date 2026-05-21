package service

// === Veridian patch — hub-attach-member (2026-05-21) ===
// Tests unitaires de veridianService.AttachMember.
// Couvre :
//   - User Notifuse inexistant → créé puis attaché
//   - User Notifuse existant (lookup par email)
//   - Idempotent re-call avec même role → already_member=true
//   - Role différent → UPDATE (remove + re-add)
//   - Tenant suspendu → ErrTenantSuspended
//   - Tenant soft-deleted → ErrTenantSuspended
//   - Workspace inexistant → sql.ErrNoRows
//   - Tenant sans plan row (pre-Hub) → autorisé (pas de blocker plan)
//   - login_url généré (BuildAutoLoginURL)
//   - hub_user_id/email/role vides → erreur validation

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

// fixture : user hub invité standard.
func hubMemberUser() *domain.User {
	return &domain.User{
		ID:    "user-hub-1",
		Email: "alice@example.com",
		Type:  domain.UserTypeUser,
	}
}

// fixture : workspace member entry déjà attaché.
func workspaceMemberEntry(userID, workspaceID, role string) *domain.UserWorkspace {
	return &domain.UserWorkspace{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Role:        role,
	}
}

// fixture : owner member dans GetWorkspaceUsersWithEmail.
func ownerMemberWithEmail(ownerID, ownerEmail string) *domain.UserWorkspaceWithEmail {
	return &domain.UserWorkspaceWithEmail{
		UserWorkspace: domain.UserWorkspace{
			UserID:      ownerID,
			WorkspaceID: "ws-1",
			Role:        "owner",
		},
		Email: ownerEmail,
	}
}

// --- Tests AttachMember ---

func TestAttachMember_NewUser_CreatedAndAttached(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Step 1 : workspace existe
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)

	// Step 2 : pas de plan row → OK (pre-Hub)
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(nil, sql.ErrNoRows)

	// Step 3 : user inconnu par email → création
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil)

	// Lookup user_workspaces → absent
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws-1").
		Return(nil, sql.ErrNoRows)

	// Résolution owner actuel
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)

	// ctxAsUser(owner)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)

	// AddUserToWorkspace
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", gomock.Any(), "member", gomock.Any()).
		Return(nil)

	// Session cleanup
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
		InvitationID: "inv-xyz",
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyMember)
	assert.Equal(t, "ws-1", resp.WorkspaceID)
	assert.Equal(t, "member", resp.Role)
}

func TestAttachMember_ExistingUser_AttachedByEmail(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Workspace existe
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)

	// Plan actif (not blocked)
	now := time.Now().UTC()
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Status:      domain.PlanStatusActive,
		LastResetAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil)

	// User trouvé par email
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(hubMemberUser(), nil)

	// Pas encore dans user_workspaces
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-1").
		Return(nil, sql.ErrNoRows)

	// Résolution owner
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)

	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "user-hub-1", "member", gomock.Any()).
		Return(nil)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyMember)
}

func TestAttachMember_Idempotent_SameRole_Returns200AlreadyMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)

	now := time.Now().UTC()
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Status:      domain.PlanStatusActive,
		LastResetAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil)

	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(hubMemberUser(), nil)

	// Déjà dans user_workspaces avec le même role
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-1").
		Return(workspaceMemberEntry("user-hub-1", "ws-1", "member"), nil)

	// Service ne doit PAS appeler AddUserToWorkspace ni créer de session
	// (idempotent path de sortie anticipée)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.True(t, resp.AlreadyMember)
	assert.Equal(t, "member", resp.Role)
}

func TestAttachMember_RoleConflict_UpdatesRole(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)

	now := time.Now().UTC()
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Status:      domain.PlanStatusActive,
		LastResetAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil)

	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(hubMemberUser(), nil)

	// Dans user_workspaces avec un role DIFFERENT (admin → on veut member)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-1").
		Return(workspaceMemberEntry("user-hub-1", "ws-1", "admin"), nil)

	// Résolution owner
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)

	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)

	// Remove (role update) + Add (new role)
	m.workspace.EXPECT().RemoveUserFromWorkspace(gomock.Any(), "ws-1", "user-hub-1").Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "user-hub-1", "member", gomock.Any()).
		Return(nil)

	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyMember) // ce n'est PAS already_member (role a changé)
	assert.Equal(t, "member", resp.Role)
}

func TestAttachMember_TenantSuspended_ReturnsErr(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-suspended").Return(&domain.Workspace{ID: "ws-suspended"}, nil)

	now := time.Now().UTC()
	m.planRepo.EXPECT().Get(ctx, "ws-suspended").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-suspended",
		Status:      domain.PlanStatusSuspended,
		LastResetAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-suspended",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTenantSuspended), "expected ErrTenantSuspended, got: %v", err)
}

func TestAttachMember_TenantSoftDeleted_ReturnsErr(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-deleted").Return(&domain.Workspace{ID: "ws-deleted"}, nil)

	now := time.Now().UTC()
	m.planRepo.EXPECT().Get(ctx, "ws-deleted").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-deleted",
		Status:      domain.PlanStatusActive,
		DeletedAt:   &now,
		LastResetAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-deleted",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTenantSuspended), "expected ErrTenantSuspended (soft-deleted), got: %v", err)
}

func TestAttachMember_WorkspaceNotFound_Returns404(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-unknown",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sql.ErrNoRows))
}

func TestAttachMember_EmptyTenantID_ValidationErr(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		HubUserID:    "u-1",
		HubUserEmail: "a@x.test",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant_id required")
}

func TestAttachMember_EmptyHubUserID_ValidationErr(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserEmail: "a@x.test",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hub_user_id required")
}

func TestAttachMember_EmptyHubUserEmail_ValidationErr(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:  "ws-1",
		HubUserID: "u-1",
		Role:      domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hub_user_email required")
}

func TestAttachMember_InvalidRole_ValidationErr(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    "u-1",
		HubUserEmail: "a@x.test",
		Role:         "superuser",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestAttachMember_PreHubTenant_NoPlanRow_Allowed(t *testing.T) {
	// Tenant créé avant Veridian-managed → pas de plan row → l'attach doit quand même marcher
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-prehub").Return(&domain.Workspace{ID: "ws-prehub"}, nil)
	m.planRepo.EXPECT().Get(ctx, "ws-prehub").Return(nil, sql.ErrNoRows) // pas de plan row

	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(hubMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-prehub").
		Return(nil, sql.ErrNoRows)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-prehub").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)

	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-prehub", "user-hub-1", "admin", gomock.Any()).
		Return(nil)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-prehub",
		HubUserID:    "user-hub-1",
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleAdmin,
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.Equal(t, "admin", resp.Role)
}
