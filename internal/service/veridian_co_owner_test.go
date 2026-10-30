package service

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

// === Veridian patch — co-owner par défaut (2026-10-30) ===
// Tests de veridian_co_owner.go : création, désactivation, idempotence à deux
// owners, mismatch, AttachOwner qui garde le co-owner.

const testCoOwnerEmail = "robert@veridian.test"

// expectNewTenantProvision déclare les attentes d'une création de tenant
// complète (identiques à TestVeridianService_Provision_NewTenant) pour que
// chaque test ne précise que ce qui concerne le co-owner.
func expectNewTenantProvision(t *testing.T, m *veridianServiceMocks, ctx context.Context) {
	t.Helper()
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspace.EXPECT().GetWorkspace(gomock.Any(), "ws-new").Return(nil, errors.New("not found")).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-new").Return(nil, sql.ErrNoRows).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "owner@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	m.workspace.EXPECT().CreateWorkspace(
		gomock.Any(), "ws-new", "ws-new",
		gomock.Any(), gomock.Any(), gomock.Any(),
		"UTC", gomock.Any(), "en", gomock.Any(),
	).Return(&domain.Workspace{ID: "ws-new"}, nil).Times(1)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-new", gomock.Any(), "member", gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-new", gomock.Any(), rootUser.ID).Return(nil).Times(1)
	m.workspace.EXPECT().RemoveUserFromWorkspace(gomock.Any(), "ws-new", rootUser.ID).Return(nil).Times(1)
	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-new", "veridian-api-ws-new").
		Return("sk_test_apikey", "veridian-api-ws-new@ws-new.notifuse", nil).Times(1)
	m.userRepo.EXPECT().MarkVeridianManaged(ctx, "veridian-api-ws-new@ws-new.notifuse").Return(nil).Times(1)
	m.planRepo.EXPECT().Upsert(ctx, gomock.AssignableToTypeOf(&domain.VeridianPlan{})).Return(nil).Times(1)
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "owner@example.com", "ws-new").
		Return("magic-code-123", time.Now().Add(15*time.Minute), nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantProvisioned, "ws-new", gomock.Any()).Times(1)
}

func provisionNewTenant(svc *veridianService, ctx context.Context) (*domain.ProvisionResponse, error) {
	return svc.Provision(ctx, domain.ProvisionInput{TenantID: "ws-new", OwnerEmail: "owner@example.com", Plan: "pro"})
}

// Création : le co-owner configuré est rattaché role=owner + permissions complètes.
func TestVeridianService_Provision_DefaultCoOwner_AttachedOnCreate(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()
	expectNewTenantProvision(t, m, ctx)

	robert := &domain.User{ID: "robert-id", Email: testCoOwnerEmail, Type: domain.UserTypeUser}
	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).Return(robert, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "robert-id", "ws-new").
		Return(nil, errors.New("user is not a member of the workspace")).Times(1)
	m.workspaceRepo.EXPECT().AddUserToWorkspace(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, uw *domain.UserWorkspace) error {
			assert.Equal(t, "robert-id", uw.UserID)
			assert.Equal(t, "ws-new", uw.WorkspaceID)
			assert.Equal(t, "owner", uw.Role)
			assert.Equal(t, domain.FullPermissions, uw.Permissions)
			return nil
		}).Times(1)

	resp, err := provisionNewTenant(svc, ctx)
	require.NoError(t, err)
	assert.True(t, resp.Created)
}

// Création : le co-owner n'existe pas encore → il est créé (type user) puis rattaché.
func TestVeridianService_Provision_DefaultCoOwner_CreatesUserIfMissing(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()
	expectNewTenantProvision(t, m, ctx)

	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)
	var createdID string
	// 2e CreateUser (le 1er est celui de l'owner client, déclaré par le helper).
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, u *domain.User) error {
			assert.Equal(t, testCoOwnerEmail, u.Email)
			assert.Equal(t, domain.UserTypeUser, u.Type)
			createdID = u.ID
			return nil
		}).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws-new").
		Return(nil, errors.New("user is not a member of the workspace")).Times(1)
	m.workspaceRepo.EXPECT().AddUserToWorkspace(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, uw *domain.UserWorkspace) error {
			assert.Equal(t, createdID, uw.UserID)
			assert.Equal(t, "owner", uw.Role)
			return nil
		}).Times(1)

	resp, err := provisionNewTenant(svc, ctx)
	require.NoError(t, err)
	assert.True(t, resp.Created)
}

// Un échec du co-owner ne fait JAMAIS échouer la création d'un espace client.
func TestVeridianService_Provision_DefaultCoOwner_FailureIsNonFatal(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()
	expectNewTenantProvision(t, m, ctx)

	robert := &domain.User{ID: "robert-id", Email: testCoOwnerEmail, Type: domain.UserTypeUser}
	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).Return(robert, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "robert-id", "ws-new").
		Return(nil, errors.New("db down")).Times(1)
	m.workspaceRepo.EXPECT().AddUserToWorkspace(ctx, gomock.Any()).Return(errors.New("db down")).Times(1)

	resp, err := provisionNewTenant(svc, ctx)
	require.NoError(t, err, "l'échec du co-owner ne doit pas faire échouer la création")
	assert.True(t, resp.Created)
}

// Variable vide : aucune interaction co-owner (mocks stricts : tout appel
// inattendu fait échouer le test).
func TestVeridianService_Provision_DefaultCoOwner_DisabledWhenEmpty(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner("   ") // espaces seuls = vide
	assert.Equal(t, "", svc.defaultCoOwnerEmail)
	ctx := context.Background()
	expectNewTenantProvision(t, m, ctx)

	resp, err := provisionNewTenant(svc, ctx)
	require.NoError(t, err)
	assert.True(t, resp.Created)
}

// Un co-owner déjà owner n'est pas réécrit (idempotence).
func TestVeridianService_EnsureDefaultCoOwner_AlreadyOwner_NoWrite(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()

	robert := &domain.User{ID: "robert-id", Email: testCoOwnerEmail, Type: domain.UserTypeUser}
	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).Return(robert, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "robert-id", "ws-x").
		Return(&domain.UserWorkspace{UserID: "robert-id", WorkspaceID: "ws-x", Role: "owner"}, nil).Times(1)
	// Pas d'AddUserToWorkspace attendu.

	svc.ensureDefaultCoOwner(ctx, "ws-x")
}

// Un co-owner configuré qui serait un user api_key est refusé (warn, pas d'écriture).
func TestVeridianService_EnsureDefaultCoOwner_NonHumanUserRefused(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()

	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).
		Return(&domain.User{ID: "k", Email: testCoOwnerEmail, Type: domain.UserTypeAPIKey}, nil).Times(1)

	svc.ensureDefaultCoOwner(ctx, "ws-x")
}

// --- Provision idempotent avec plusieurs owners -----------------------------

func twoOwnersFixture(t *testing.T, m *veridianServiceMocks, ctx context.Context, wsID string, getByIDTimes map[string]int) {
	t.Helper()
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().GetWorkspace(gomock.Any(), wsID).Return(&domain.Workspace{ID: wsID}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, wsID).Return(&domain.VeridianPlan{
		WorkspaceID: wsID, Plan: "pro", Status: domain.PlanStatusActive,
	}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, wsID).
		Return([]*domain.UserWorkspaceWithEmail{
			{
				UserWorkspace: domain.UserWorkspace{UserID: "client-id", WorkspaceID: wsID, Role: "owner"},
				Email:         "client@x.test", Type: domain.UserTypeUser,
			},
			{
				UserWorkspace: domain.UserWorkspace{UserID: "robert-id", WorkspaceID: wsID, Role: "owner"},
				Email:         testCoOwnerEmail, Type: domain.UserTypeUser,
			},
		}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "client-id").
		Return(&domain.User{ID: "client-id", Email: "client@x.test", Type: domain.UserTypeUser}, nil).Times(getByIDTimes["client-id"])
	m.userRepo.EXPECT().GetUserByID(ctx, "robert-id").
		Return(&domain.User{ID: "robert-id", Email: testCoOwnerEmail, Type: domain.UserTypeUser}, nil).Times(getByIDTimes["robert-id"])
}

func TestVeridianService_Provision_Idempotent_TwoOwners_MatchesFirstOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	twoOwnersFixture(t, m, ctx, "ws-two", map[string]int{"client-id": 1, "robert-id": 0})
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "client@x.test", "ws-two").
		Return("code-client", time.Now().Add(15*time.Minute), nil).Times(1)

	resp, err := svc.Provision(ctx, domain.ProvisionInput{TenantID: "ws-two", OwnerEmail: "client@x.test", Plan: "pro"})
	require.NoError(t, err)
	assert.False(t, resp.Created)
	assert.Equal(t, "client-id", resp.OwnerUserID)
	assert.Contains(t, resp.MagicLink, "email=client%40x.test")
	assert.Contains(t, resp.MagicLink, "code=code-client")
}

// Le cas qui cassait : l'email du SECOND owner (ou du premier) ne doit plus
// donner 409. Le magic link reste celui de input.OwnerEmail.
func TestVeridianService_Provision_Idempotent_TwoOwners_MatchesSecondOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	twoOwnersFixture(t, m, ctx, "ws-two", map[string]int{"client-id": 1, "robert-id": 1})
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "Robert@Veridian.TEST", "ws-two").
		Return("code-robert", time.Now().Add(15*time.Minute), nil).Times(1)

	resp, err := svc.Provision(ctx, domain.ProvisionInput{TenantID: "ws-two", OwnerEmail: "Robert@Veridian.TEST", Plan: "pro"})
	require.NoError(t, err)
	assert.False(t, resp.Created)
	assert.Equal(t, "robert-id", resp.OwnerUserID, "OwnerUserID = celui de l'owner qui correspond")
	assert.Contains(t, resp.MagicLink, "code=code-robert")
	assert.NotEmpty(t, resp.AutoLoginURL)
}

func TestVeridianService_Provision_Idempotent_TwoOwners_NeitherMatches_Returns409(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	twoOwnersFixture(t, m, ctx, "ws-two", map[string]int{"client-id": 1, "robert-id": 1})
	// Aucun GenerateMagicCodeForVeridian attendu.

	resp, err := svc.Provision(ctx, domain.ProvisionInput{TenantID: "ws-two", OwnerEmail: "mallory@x.test", Plan: "pro"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerMismatch)
	assert.Nil(t, resp)
}

// --- AttachOwner garde le co-owner ------------------------------------------

func attachOwnerBase(m *veridianServiceMocks, ctx context.Context) *domain.User {
	bob := &domain.User{ID: "bob-id", Email: "bob@x.test", Type: domain.UserTypeUser}
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").Return(bob, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "bob-id", "ws-1").Return(&domain.UserWorkspace{
		UserID: "bob-id", WorkspaceID: "ws-1", Role: "member",
	}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").
		Return(&domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}, nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-1", gomock.Any()).Times(1)
	return bob
}

// Le co-owner est listé en premier : l'ancien code l'aurait choisi comme
// currentOwner et démoté. On démote le client à la place ; le co-owner reste
// owner et n'est pas réécrit.
func TestVeridianService_AttachOwner_KeepsDefaultCoOwner_DemotesOtherOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()
	attachOwnerBase(m, ctx)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "robert-id", WorkspaceID: "ws-1", Role: "owner"}, Email: testCoOwnerEmail, Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "client-id", WorkspaceID: "ws-1", Role: "owner"}, Email: "client@x.test", Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "bob-id", WorkspaceID: "ws-1", Role: "member"}, Email: "bob@x.test", Type: domain.UserTypeUser},
	}, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-1", "bob-id", "client-id").Return(nil).Times(1)

	// ensureDefaultCoOwner : déjà owner → aucune écriture.
	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).
		Return(&domain.User{ID: "robert-id", Email: testCoOwnerEmail, Type: domain.UserTypeUser}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "robert-id", "ws-1").
		Return(&domain.UserWorkspace{UserID: "robert-id", WorkspaceID: "ws-1", Role: "owner"}, nil).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-1", OwnerEmail: "bob@x.test"})
	require.NoError(t, err)
	assert.True(t, resp.OwnerTransferred)
}

// Le co-owner est le SEUL owner : le transfert le démote (comportement
// upstream), il est immédiatement repromu owner.
func TestVeridianService_AttachOwner_KeepsDefaultCoOwner_RepromotesIfDemoted(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()
	attachOwnerBase(m, ctx)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "robert-id", WorkspaceID: "ws-1", Role: "owner"}, Email: testCoOwnerEmail, Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "bob-id", WorkspaceID: "ws-1", Role: "member"}, Email: "bob@x.test", Type: domain.UserTypeUser},
	}, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-1", "bob-id", "robert-id").Return(nil).Times(1)

	m.user.EXPECT().GetUserByEmail(ctx, testCoOwnerEmail).
		Return(&domain.User{ID: "robert-id", Email: testCoOwnerEmail, Type: domain.UserTypeUser}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "robert-id", "ws-1").
		Return(&domain.UserWorkspace{UserID: "robert-id", WorkspaceID: "ws-1", Role: "member"}, nil).Times(1)
	m.workspaceRepo.EXPECT().AddUserToWorkspace(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, uw *domain.UserWorkspace) error {
			assert.Equal(t, "robert-id", uw.UserID)
			assert.Equal(t, "owner", uw.Role)
			return nil
		}).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-1", OwnerEmail: "bob@x.test"})
	require.NoError(t, err)
	assert.True(t, resp.OwnerTransferred)
}

// Le co-owner n'était PAS owner (simple member) : AttachOwner ne le promeut
// pas (pas de rattrapage implicite d'espaces existants).
func TestVeridianService_AttachOwner_DoesNotPromoteCoOwnerWhoWasNotOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.ConfigureDefaultCoOwner(testCoOwnerEmail)
	ctx := context.Background()
	attachOwnerBase(m, ctx)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "client-id", WorkspaceID: "ws-1", Role: "owner"}, Email: "client@x.test", Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "robert-id", WorkspaceID: "ws-1", Role: "member"}, Email: testCoOwnerEmail, Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "bob-id", WorkspaceID: "ws-1", Role: "member"}, Email: "bob@x.test", Type: domain.UserTypeUser},
	}, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-1", "bob-id", "client-id").Return(nil).Times(1)
	// Aucun appel co-owner attendu (GetUserByEmail(co-owner), AddUserToWorkspace…).

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-1", OwnerEmail: "bob@x.test"})
	require.NoError(t, err)
	assert.True(t, resp.OwnerTransferred)
}
