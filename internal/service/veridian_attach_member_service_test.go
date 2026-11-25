package service

// === Veridian patch — hub-attach-member (2026-05-21) ===
// Tests unitaires de veridianService.AttachMember.
// Couvre :
//   - User Notifuse inexistant → créé puis attaché
//   - User Notifuse existant (lookup par hub_user_id SEUL, fiche 89)
//   - Adresse deja portee : reliee si compte sans lien ET adresse attestee,
//     sinon ErrHubUserIDConflict (rien n'est ajoute a l'espace)
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
	m.userRepo.EXPECT().GetUserByHubUserID(gomock.Any(), gomock.Any()).Return(nil, &domain.ErrUserNotFound{Message: "not found"}).AnyTimes()
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

	// Webhook tenant.member_added emit sur new attach (§7.1).
	// Assert sur invitation_id pour valider l'audit cross-app §5.22.5.
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "alice@example.com", data["user_email"])
			assert.Equal(t, "member", data["role"])
			assert.Equal(t, testHubUID, data["hub_user_id"])
			assert.Equal(t, "inv-xyz", data["invitation_id"])
			assert.Equal(t, "hub", data["actor"])
		}).Times(1)

	// Session cleanup
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    testHubUID,
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

func TestAttachMember_ExistingUser_FoundByHubUserID(t *testing.T) {
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

	// Personne trouvée par son hub_user_id (l'adresse ne désigne plus personne)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(hubMemberUser(), nil)

	// Pas encore dans user_workspaces
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-1").
		Return(nil, sql.ErrNoRows)

	// Résolution owner
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)

	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "user-hub-1", "member", gomock.Any()).
		Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    testHubUID,
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

	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(hubMemberUser(), nil)

	// Déjà dans user_workspaces avec le même role
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-1").
		Return(workspaceMemberEntry("user-hub-1", "ws-1", "member"), nil)

	// Service ne doit PAS appeler AddUserToWorkspace ni créer de session
	// (idempotent path de sortie anticipée)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.True(t, resp.AlreadyMember)
	assert.Equal(t, "member", resp.Role)
}

// TestAttachMember_RoleConflict_KeepsLocalRole — CONTRAT-HUB §5.22.4
// (refactor 2026-05-23) : le Hub n'est PAS autoritatif sur les roles
// internes Notifuse. Si un user est deja membre du workspace avec un
// role DIFFERENT de celui demande par le Hub, on retourne 200
// already_member=true en CONSERVANT le role local intact. PAS de UPDATE
// remove+re-add (downgrade silencieux d'un admin local interdit).
//
// Cas concret : un user a ete promu "owner" en local via Team Settings,
// puis le Hub renvoie une invitation "member" pour ce meme user → on doit
// garder son "owner" local. Sans cette protection, l'invitation Hub
// retroactive ecraserait silencieusement le role local souverain.
func TestAttachMember_RoleConflict_KeepsLocalRole(t *testing.T) {
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

	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(hubMemberUser(), nil)

	// Deja dans user_workspaces avec un role LOCAL DIFFERENT (admin local
	// souverain — promu via UI Team Settings) que celui demande par Hub.
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-1").
		Return(workspaceMemberEntry("user-hub-1", "ws-1", "admin"), nil)

	// ⚠️ Le service NE DOIT PAS appeler RemoveUserFromWorkspace ni
	// AddUserToWorkspace ni CreateSession ni emitter.Emit (pas de mutation,
	// sortie already_member anticipee — l'emit role_changed historique du
	// lot M est aussi supprime du flow AttachMember car le role local n'est
	// jamais mute via Hub depuis §5.22.4). Mocks gomock leveront un
	// "Unexpected call" si une de ces methodes est invoquee.

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember, // Hub demande "member"
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.True(t, resp.AlreadyMember, "doit etre already_member=true (no-downgrade §5.22.4)")
	assert.Equal(t, "admin", resp.Role, "role LOCAL conserve, pas ecrase par le Hub (§5.22.4)")
}

// attachMemberUnknownPerson prepare le debut commun : espace existant, sans plan, hub_user_id inconnu ici.
func attachMemberUnknownPerson(t *testing.T, m *veridianServiceMocks, ctx context.Context, ws string) {
	t.Helper()
	m.workspaceRepo.EXPECT().GetByID(ctx, ws).Return(&domain.Workspace{ID: ws}, nil)
	m.planRepo.EXPECT().Get(ctx, ws).Return(nil, sql.ErrNoRows)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, &domain.ErrUserNotFound{Message: "not found"})
}

// TestAttachMember_AddressLinkedToOtherHubUserID_Conflict_NoAdd — CONTRAT-HUB 5.19.7 : l'adresse demandee est portee ici
// par un compte DEJA relie a une autre personne du Hub. Refus ErrHubUserIDConflict, rien n'est ajoute a l'espace
// (ni GetUserWorkspace, ni AddUserToWorkspace, ni session : gomock echoue sur tout appel non attendu).
func TestAttachMember_AddressLinkedToOtherHubUserID_Conflict_NoAdd(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	attachMemberUnknownPerson(t, m, ctx, "ws-conflict")

	other := testOtherHubUID
	holder := hubMemberUser()
	holder.HubUserID = &other
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-conflict",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		EmailProven:  true,
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrHubUserIDConflict), "attendu ErrHubUserIDConflict, recu: %v", err)
}

// Compte sans lien ET adresse attestee par le Hub : relie, puis ajoute a l'espace.
func TestAttachMember_UnlinkedAccount_ProvenAddress_LinkedAndAdded(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	attachMemberUnknownPerson(t, m, ctx, "ws-link")

	holder := hubMemberUser()
	holder.HubUserID = nil
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)
	m.userRepo.EXPECT().BackfillHubUserID(ctx, "user-hub-1", testHubUID).Return(nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-link").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-link").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-link", "user-hub-1", "member", gomock.Any()).Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-link", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-link",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		EmailProven:  true,
		Role:         domain.AttachMemberRoleMember,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyMember)
}

// Compte sans lien mais adresse NON attestee : jamais relie, jamais ajoute.
func TestAttachMember_UnlinkedAccount_UnprovenAddress_Conflict(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	attachMemberUnknownPerson(t, m, ctx, "ws-unproven")

	holder := hubMemberUser()
	holder.HubUserID = nil
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)
	// Aucun BackfillHubUserID attendu.

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-unproven",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		EmailProven:  false,
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrHubUserIDConflict), "attendu ErrHubUserIDConflict, recu: %v", err)
}

// Le lien est refuse par la base (BackfillHubUserID -> ErrHubUserIDMismatch, course avec un autre lien) :
// ErrHubUserIDConflict, rien n'est ajoute a l'espace. Ancien comportement (non bloquant) supprime.
func TestAttachMember_BackfillRefused_Conflict_NoAdd(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	attachMemberUnknownPerson(t, m, ctx, "ws-mismatch")

	holder := hubMemberUser()
	holder.HubUserID = nil
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)
	m.userRepo.EXPECT().BackfillHubUserID(ctx, "user-hub-1", testHubUID).Return(domain.ErrHubUserIDMismatch)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-mismatch",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		EmailProven:  true,
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrHubUserIDConflict), "attendu ErrHubUserIDConflict, recu: %v", err)
}

func TestAttachMember_TenantSuspended_ReturnsErr(t *testing.T) {
	svc, m := newVeridianService(t)
	m.userRepo.EXPECT().GetUserByHubUserID(gomock.Any(), gomock.Any()).Return(nil, &domain.ErrUserNotFound{Message: "not found"}).AnyTimes()
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
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTenantSuspended), "expected ErrTenantSuspended, got: %v", err)
}

func TestAttachMember_TenantSoftDeleted_ReturnsErr(t *testing.T) {
	svc, m := newVeridianService(t)
	m.userRepo.EXPECT().GetUserByHubUserID(gomock.Any(), gomock.Any()).Return(nil, &domain.ErrUserNotFound{Message: "not found"}).AnyTimes()
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
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTenantSuspended), "expected ErrTenantSuspended (soft-deleted), got: %v", err)
}

func TestAttachMember_WorkspaceNotFound_Returns404(t *testing.T) {
	svc, m := newVeridianService(t)
	m.userRepo.EXPECT().GetUserByHubUserID(gomock.Any(), gomock.Any()).Return(nil, &domain.ErrUserNotFound{Message: "not found"}).AnyTimes()
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-unknown",
		HubUserID:    testHubUID,
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

	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(hubMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "user-hub-1", "ws-prehub").
		Return(nil, sql.ErrNoRows)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-prehub").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)

	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	// Le role Hub 'admin' est mappé vers 'member' côté Notifuse : upstream
	// n'a QUE owner|member en role workspace (workspace_service refuse tout
	// autre). CONTRAT-HUB §3.5 — le Hub n'est pas autoritatif sur les rôles
	// internes app. Régression-guard du bug 2026-05-22 (500 'role must be
	// owner or member').
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-prehub", "user-hub-1", "member", gomock.Any()).
		Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-prehub", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-prehub",
		HubUserID:    testHubUID,
		HubUserEmail: "alice@example.com",
		Role:         domain.AttachMemberRoleAdmin,
	})
	require.NoError(t, err)
	assert.True(t, resp.Attached)
	assert.Equal(t, "member", resp.Role, "role Hub 'admin' mappé vers 'member' Notifuse")
}

// TestAttachMember_NewUser_PassesHubUserIDToCreateUser — V46 §3.7
// regression guard. Bug 2026-05-23 : AttachMember constructait bien un
// User avec HubUserID set, mais userRepository.CreateUser ne le persistait
// pas en DB (INSERT n'incluait pas la colonne) → /api/user.me renvoyait
// undefined. Ce test capture l'argument passé a CreateUser et asserte que
// HubUserID est bien set, pour attraper la regression au niveau service.
func TestAttachMember_NewUser_PassesHubUserIDToCreateUser(t *testing.T) {
	svc, m := newVeridianService(t)
	m.userRepo.EXPECT().GetUserByHubUserID(gomock.Any(), gomock.Any()).Return(nil, &domain.ErrUserNotFound{Message: "not found"}).AnyTimes()
	ctx := context.Background()

	hubID := "11111111-2222-3333-4444-555555555555"
	email := "newmember@huid.test"

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(nil, sql.ErrNoRows)

	// User absent → AttachMember doit construire un &User{HubUserID:&hubID}
	// et l'envoyer a CreateUser. On capture le user via Do() pour asserter.
	m.user.EXPECT().GetUserByEmail(ctx, email).
		Return(nil, &domain.ErrUserNotFound{Message: "not found"})
	var captured *domain.User
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).
		Do(func(_ context.Context, u *domain.User) {
			captured = u
		}).
		Return(nil)

	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws-1").
		Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{ownerMemberWithEmail("owner-1", "owner@ws.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", gomock.Any(), "member", gomock.Any()).
		Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	_, err := svc.AttachMember(ctx, domain.AttachMemberInput{
		TenantID:     "ws-1",
		HubUserID:    hubID,
		HubUserEmail: email,
		Role:         domain.AttachMemberRoleMember,
	})
	require.NoError(t, err)
	require.NotNil(t, captured, "CreateUser doit etre appele avec le user nouveau membre")
	assert.Equal(t, email, captured.Email)
	require.NotNil(t, captured.HubUserID,
		"V46 §3.7 — HubUserID DOIT etre set sur le user passe a CreateUser, sinon le binding Hub est perdu en DB (bug 2026-05-23)")
	assert.Equal(t, hubID, *captured.HubUserID,
		"HubUserID doit refleter exactement input.HubUserID du Hub")
}

func TestAttachMember_EmailChanged_ReusesUserBoundByHubUserID(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	hubID := "11111111-2222-3333-4444-555555555555"
	bound := &domain.User{ID: "u-bound", Email: "ancien@x.test", Type: domain.UserTypeUser, HubUserID: &hubID}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(nil, sql.ErrNoRows)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, hubID).Return(bound, nil)
	// Pas de GetUserByEmail ni de CreateUser : la personne est son hub_user_id, un second user violerait l'index unique V70.
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "u-bound", "ws-1").Return(&domain.UserWorkspace{UserID: "u-bound", WorkspaceID: "ws-1", Role: "member"}, nil)

	resp, err := svc.AttachMember(ctx, domain.AttachMemberInput{TenantID: "ws-1", HubUserID: hubID, HubUserEmail: "nouveau@x.test", Role: "member"})
	require.NoError(t, err)
	assert.True(t, resp.AlreadyMember)
}
