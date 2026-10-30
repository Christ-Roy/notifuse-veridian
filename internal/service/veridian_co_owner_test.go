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
