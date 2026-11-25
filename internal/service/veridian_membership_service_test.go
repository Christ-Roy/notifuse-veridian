package service

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
// Tests unitaires de veridianService.SyncMember, RemoveMember, RestoreMember.
//
// Couvre :
//   - SyncMember : new user, existing user, idempotent same role, owner kept
//                  no-downgrade, tenant not found, validation inputs
//   - RemoveMember : success, owner refused, idempotent unknown person (removed=false),
//                    idempotent not member, tenant not found
//   - RestoreMember : unknown person (ErrUserNotInApp, never created), success
//                     existing user, idempotent already member, tenant not found

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

// testHubUID : un hub_user_id valide (UUID) pour les fixtures de la personne du Hub (fiche 89).
const testHubUID = "6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01"

// testOtherHubUID : une autre personne du Hub.
const testOtherHubUID = "0a8d9e72-5b34-4f6a-8c21-7e3b9d4a6f02"

// fixture : user member type=user, relie a testHubUID.
func membershipMemberUser() *domain.User {
	id := testHubUID
	return &domain.User{
		ID:        "member-uuid-1",
		Email:     "alice@example.com",
		Type:      domain.UserTypeUser,
		HubUserID: &id,
	}
}

// fixture : le Hub ne connait pas cette personne ici.
func hubUnknown() *domain.ErrUserNotFound { return &domain.ErrUserNotFound{Message: "not found"} }

// === SyncMember ============================================================

func TestSyncMember_NewUser_CreatedAndAttached(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)

	// Personne inconnue (hub_user_id), adresse libre → CreateUser relie au hub_user_id.
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, hubUnknown())
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(nil, hubUnknown())
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, u *domain.User) error {
		require.NotNil(t, u.HubUserID)
		assert.Equal(t, testHubUID, *u.HubUserID)
		assert.Equal(t, "alice@example.com", u.Email)
		return nil
	})

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

	// Webhook tenant.member_added emit sur new attach (§7.1).
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)

	// Cleanup.
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		HubUserID: testHubUID,
		Role:      domain.SyncMemberRoleMember,
	})
	require.NoError(t, err)
	assert.True(t, resp.Synced)
	assert.Equal(t, "member", resp.AppRole)
	assert.NotEmpty(t, resp.AppUserID)
}

func TestSyncMember_ExistingUser_FoundByHubUserID(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "member-uuid-1", "member", gomock.Any()).Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		HubUserID: testHubUID,
		Role:      domain.SyncMemberRoleAdmin, // Hub envoie admin, on mappe en member cote Notifuse
	})
	require.NoError(t, err)
	assert.Equal(t, "member", resp.AppRole, "Hub admin → Notifuse member (workspace upstream sans admin)")
}

// Adresse deja portee ici par un compte SANS lien et attestee par le Hub : reliee puis ajoutee.
func TestSyncMember_AddressHeldByUnlinkedAccount_Proven_LinkedAndAdded(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	holder := &domain.User{ID: "member-uuid-1", Email: "alice@example.com", Type: domain.UserTypeUser}
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, hubUnknown())
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)
	m.userRepo.EXPECT().BackfillHubUserID(ctx, "member-uuid-1", testHubUID).Return(nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "member-uuid-1", "member", gomock.Any()).Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID: "ws-1", UserEmail: "alice@example.com", HubUserID: testHubUID, EmailProven: true,
		Role: domain.SyncMemberRoleMember,
	})
	require.NoError(t, err)
	assert.Equal(t, "member-uuid-1", resp.AppUserID)
}

// Compte sans lien mais adresse NON attestee : jamais relie, jamais ajoute.
func TestSyncMember_AddressHeldByUnlinkedAccount_NotProven_Conflict(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	holder := &domain.User{ID: "member-uuid-1", Email: "alice@example.com", Type: domain.UserTypeUser}
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, hubUnknown())
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)
	// Aucun BackfillHubUserID, aucun GetUserWorkspace, aucun AddUserToWorkspace attendus.

	_, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID: "ws-1", UserEmail: "alice@example.com", HubUserID: testHubUID, EmailProven: false,
		Role: domain.SyncMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrHubUserIDConflict))
}

// Adresse portee par un compte deja lie a UNE AUTRE personne du Hub : conflit, rien n'est ajoute a l'espace.
func TestSyncMember_AddressHeldByOtherHubUserID_Conflict_NoAdd(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	other := testOtherHubUID
	holder := &domain.User{ID: "member-uuid-1", Email: "alice@example.com", Type: domain.UserTypeUser, HubUserID: &other}
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, hubUnknown())
	m.user.EXPECT().GetUserByEmail(ctx, "alice@example.com").Return(holder, nil)

	_, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID: "ws-1", UserEmail: "alice@example.com", HubUserID: testHubUID, EmailProven: true,
		Role: domain.SyncMemberRoleMember,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrHubUserIDConflict))
}

func TestSyncMember_Idempotent_AlreadyMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").
		Return(membershipEntry("member-uuid-1", "ws-1", "member"), nil)
	// Pas d'AddUserToWorkspace, pas de session — idempotent path.

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		HubUserID: testHubUID,
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
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).
		Return(&domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}, nil)
	// Le user est DEJA owner → service garde owner (additif, jamais downgrade).
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "owner-uuid", "ws-1").
		Return(membershipEntry("owner-uuid", "ws-1", "owner"), nil)

	resp, err := svc.SyncMember(ctx, domain.SyncMemberInput{
		TenantID:  "ws-1",
		UserEmail: "owner@x.test",
		HubUserID: testHubUID,
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
		HubUserID: testHubUID,
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
		{"missing tenant", domain.SyncMemberInput{UserEmail: "a@x", HubUserID: testHubUID, Role: domain.SyncMemberRoleMember}},
		{"missing hub_user_id", domain.SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x", Role: domain.SyncMemberRoleMember}},
		{"invalid role", domain.SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x", HubUserID: testHubUID, Role: "superuser"}},
		{"owner role refused", domain.SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x", HubUserID: testHubUID, Role: "owner"}},
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
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").
		Return(membershipEntry("member-uuid-1", "ws-1", "member"), nil)
	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil)
	key := func(kind domain.UserType, managed bool) *domain.UserWorkspaceWithEmail {
		return &domain.UserWorkspaceWithEmail{Type: kind, VeridianManaged: managed}
	}
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		key(domain.UserTypeUser, false), key(domain.UserTypeAPIKey, false), key(domain.UserTypeAPIKey, false), key(domain.UserTypeAPIKey, true)}, nil)

	// Webhook tenant.member_removed emit sur hard delete reussi (§7.1).
	// Asserter sur le payload : reason + actor + user_email obligatoires.
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberRemoved, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "alice@example.com", data["user_email"])
			assert.Equal(t, "admin_action", data["reason"])
			assert.Equal(t, "hub", data["actor"])
			assert.Equal(t, "member-uuid-1", data["app_user_id"])
		}).Times(1)

	resp, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
		Reason:    "admin_action",
	})
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.True(t, resp.Removed)
	assert.Equal(t, testHubUID, resp.HubUserID)
	assert.False(t, resp.RemovedAt.IsZero())
	require.NotNil(t, resp.ActiveAPIKeys)
	assert.Equal(t, 2, *resp.ActiveAPIKeys, "ni l'humain, ni la clé du Hub")
	assert.Regexp(t, `^https://[^/]+/console/workspace/ws-1/settings/team$`, resp.APIKeysURL)
}

func TestRemoveMember_Owner_ReturnsErrCannotRemoveOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).
		Return(&domain.User{ID: "owner-uuid", Email: "owner@x.test", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "owner-uuid", "ws-1").
		Return(membershipEntry("owner-uuid", "ws-1", "owner"), nil)
	// RemoveUserFromWorkspace NE doit PAS etre appele (refus prealable).

	_, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCannotRemoveOwner))
}

func TestRemoveMember_Idempotent_UnknownUser(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, hubUnknown())
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return(nil, errors.New("base coupée"))
	// Pas de GetUserByEmail (l'adresse ne designe personne), pas de GetUserWorkspace,
	// pas de RemoveUserFromWorkspace — idempotent return, 200 removed=false.

	resp, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
		UserEmail: "ghost@x.test",
	})
	require.NoError(t, err, "un comptage en échec ne défait pas le retrait")
	assert.False(t, resp.Removed, "personne inconnue : rien retiré")
	assert.Nil(t, resp.ActiveAPIKeys, "non compté, jamais un faux zéro")
	assert.Empty(t, resp.APIKeysURL)
	assert.False(t, resp.RemovedAt.IsZero())
}

func TestRemoveMember_Idempotent_NotMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return(nil, nil)

	resp, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
	})
	require.NoError(t, err)
	assert.False(t, resp.Removed, "pas membre : rien retiré")
	assert.False(t, resp.RemovedAt.IsZero())
}

func TestRemoveMember_WorkspaceNotFound_ReturnsErrNoRows(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-unknown").Return(nil, sql.ErrNoRows)

	_, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{
		TenantID:  "ws-unknown",
		HubUserID: testHubUID,
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
		{"missing tenant", domain.RemoveMemberInput{HubUserID: testHubUID}},
		{"missing hub_user_id", domain.RemoveMemberInput{TenantID: "ws-1", UserEmail: "a@x"}},
	}
	for _, tc := range cases {
		_, err := svc.RemoveMember(ctx, tc.input)
		assert.Error(t, err, tc.name)
	}
}

// === RestoreMember =========================================================

func TestRestoreMember_UnknownPerson_ErrUserNotInApp_NoCreate(t *testing.T) {
	// Personne inconnue ici : jamais de creation au restore (fiche 89). Ni recherche par adresse, ni CreateUser
	// (gomock echoue sur tout appel non attendu).
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(nil, hubUnknown())

	_, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
		UserEmail: "alice@example.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUserNotInApp))
}

func TestRestoreMember_Success_ExistingUser(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").Return(nil, sql.ErrNoRows)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").
		Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("owner-1", "owner@x.test")}, nil)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", "member-uuid-1", "member", gomock.Any()).Return(nil)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantMemberAdded, "ws-1", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil)

	resp, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
	})
	require.NoError(t, err)
	assert.True(t, resp.Restored)
}

func TestRestoreMember_Idempotent_AlreadyMember(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil)
	m.userRepo.EXPECT().GetUserByHubUserID(ctx, testHubUID).Return(membershipMemberUser(), nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "member-uuid-1", "ws-1").
		Return(membershipEntry("member-uuid-1", "ws-1", "member"), nil)
	// Pas d'AddUserToWorkspace.

	resp, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{
		TenantID:  "ws-1",
		HubUserID: testHubUID,
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
		HubUserID: testHubUID,
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
		{"missing tenant", domain.RestoreMemberInput{HubUserID: testHubUID}},
		{"missing hub_user_id", domain.RestoreMemberInput{TenantID: "ws-1", UserEmail: "a@x"}},
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
