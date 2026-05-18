package service

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

type veridianServiceMocks struct {
	workspace     *mocks.MockWorkspaceServiceInterface
	workspaceRepo *mocks.MockWorkspaceRepository
	user          *mocks.MockUserServiceInterface
	userRepo      *mocks.MockUserRepository
	planRepo      *mocks.MockVeridianPlanRepository
	emitter       *mocks.MockWebhookEmitter
}

func newVeridianService(t *testing.T) (*veridianService, *veridianServiceMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := &veridianServiceMocks{
		workspace:     mocks.NewMockWorkspaceServiceInterface(ctrl),
		workspaceRepo: mocks.NewMockWorkspaceRepository(ctrl),
		user:          mocks.NewMockUserServiceInterface(ctrl),
		userRepo:      mocks.NewMockUserRepository(ctrl),
		planRepo:      mocks.NewMockVeridianPlanRepository(ctrl),
		emitter:       mocks.NewMockWebhookEmitter(ctrl),
	}

	svc := &veridianService{
		workspaceService: m.workspace,
		workspaceRepo:    m.workspaceRepo,
		userService:      m.user,
		userRepo:         m.userRepo,
		planRepo:         m.planRepo,
		emitter:          m.emitter,
		defaultPlan:      "free",
		rootEmail:        "root@veridian.site",
		apiEndpoint:      "https://notifuse.app.veridian.site",
		hubSecret:        "test-hub-secret-32chars-min-len-ok-padding",
		logger:           logger.NewLogger(),
	}
	return svc, m
}

func TestVeridianService_New_DefaultsPlanToFree(t *testing.T) {
	svc := NewVeridianService(nil, nil, nil, nil, nil, nil, "", "root@x", "http://x", "test-hub-secret", logger.NewLogger())
	require.NotNil(t, svc)
	concrete := svc.(*veridianService)
	assert.Equal(t, "free", concrete.defaultPlan)
}

func TestVeridianService_Provision_NewTenant(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	// Tenant inexistant : workspace + plan absents
	// === Veridian patch === GetWorkspace est appele avec ctx root (lookup idempotence)
	m.workspace.EXPECT().GetWorkspace(gomock.Any(), "ws-new").Return(nil, errors.New("not found")).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-new").Return(nil, sql.ErrNoRows).Times(1)

	// Owner pas trouve par UserService
	m.user.EXPECT().GetUserByEmail(ctx, "owner@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)

	// Creation owner user
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil).Times(1)

	// 2 ctxAsRoot : 1 pour le lookup d'idempotence, 1 pour les operations workspace.
	// Plus 1 ctxAsUser (tenant owner) pour retirer root du workspace apres transfer.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(3)

	// Workspace creation
	m.workspace.EXPECT().CreateWorkspace(
		gomock.Any(), "ws-new", "ws-new",
		gomock.Any(), gomock.Any(), gomock.Any(),
		"UTC", gomock.Any(), "en", gomock.Any(),
	).Return(&domain.Workspace{ID: "ws-new"}, nil).Times(1)

	// === Veridian patch === Owner natif via TransferOwnership :
	// 1) AddUserToWorkspace(role=member) — TransferOwnership exige newOwner=member
	// 2) TransferOwnership(workspaceID, tenantUserID, rootUserID)
	// 3) RemoveUserFromWorkspace(rootUserID) depuis ctx tenant user
	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-new", gomock.Any(), "member", gomock.Any(),
	).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-new", gomock.Any(), rootUser.ID,
	).Return(nil).Times(1)
	m.workspace.EXPECT().RemoveUserFromWorkspace(
		gomock.Any(), "ws-new", rootUser.ID,
	).Return(nil).Times(1)

	// CreateAPIKey — prefix unique par tenant (sinon conflit user already exists)
	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-new", "veridian-api-ws-new").
		Return("sk_test_apikey", "veridian-api-ws-new@ws-new.notifuse", nil).Times(1)

	// Upsert plan
	m.planRepo.EXPECT().Upsert(ctx, gomock.AssignableToTypeOf(&domain.VeridianPlan{})).
		DoAndReturn(func(_ context.Context, p *domain.VeridianPlan) error {
			assert.Equal(t, "ws-new", p.WorkspaceID)
			assert.Equal(t, "pro", p.Plan)
			assert.Equal(t, domain.PlanStatusActive, p.Status)
			assert.Equal(t, int64(10000), p.MonthlyEmailQuota)
			return nil
		}).Times(1)

	// === Veridian patch === Magic link self-contained : code en clair retourne
	// systematiquement (prod ET dev), pas d'envoi email par cette voie privilegiee.
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "owner@example.com", "ws-new").
		Return("magic-code-123", time.Now().Add(15*time.Minute), nil).Times(1)

	// Webhook emit
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantProvisioned, "ws-new", gomock.Any()).Times(1)

	// Cleanup session : 3 (lookup idempotence + ops workspace + tenant ctx pour
	// retirer root via RemoveUserFromWorkspace).
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(3)

	resp, err := svc.Provision(ctx, domain.ProvisionInput{
		TenantID:   "ws-new",
		OwnerEmail: "owner@example.com",
		Plan:       "pro",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-new", resp.WorkspaceID)
	assert.Equal(t, "sk_test_apikey", resp.APIKey)
	assert.Equal(t, "veridian-api-ws-new@ws-new.notifuse", resp.APIKeyEmail)
	assert.Equal(t, "pro", resp.Plan)
	assert.True(t, resp.Created)
	assert.Contains(t, resp.MagicLink, "https://notifuse.app.veridian.site/console/signin")
	assert.Contains(t, resp.MagicLink, "code=magic-code-123")
	assert.Contains(t, resp.MagicLink, "email=owner%40example.com")
}

func TestVeridianService_Provision_Idempotent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	// === Veridian patch === Lookup idempotence : ctxAsRoot + GetWorkspace + DeleteSession
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	// Tenant existe deja : workspace + plan presents
	m.workspace.EXPECT().GetWorkspace(gomock.Any(), "ws-existing").
		Return(&domain.Workspace{ID: "ws-existing"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-existing").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-existing",
		Plan:        "pro",
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)

	m.user.EXPECT().GetUserByEmail(ctx, "owner@example.com").
		Return(&domain.User{ID: "user-1", Email: "owner@example.com"}, nil).Times(1)

	resp, err := svc.Provision(ctx, domain.ProvisionInput{
		TenantID:   "ws-existing",
		OwnerEmail: "owner@example.com",
		Plan:       "pro",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Created, "should be idempotent")
	assert.Equal(t, "ws-existing", resp.WorkspaceID)
	assert.Equal(t, "user-1", resp.OwnerUserID)
	assert.Equal(t, "pro", resp.Plan)
	assert.Empty(t, resp.APIKey, "must NOT regenerate API key on idempotent call")
}

func TestVeridianService_Provision_RejectsEmptyInput(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.Provision(ctx, domain.ProvisionInput{TenantID: "", OwnerEmail: "x@y.z"})
	assert.ErrorContains(t, err, "tenant_id required")

	_, err = svc.Provision(ctx, domain.ProvisionInput{TenantID: "ws-1", OwnerEmail: ""})
	assert.ErrorContains(t, err, "owner_email required")
}

func TestVeridianService_Suspend_EmitsEvent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Suspend(ctx, "ws-1", "non-payment").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantSuspended, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "non-payment", data["reason"])
		}).Times(1)

	err := svc.Suspend(ctx, domain.SuspendInput{TenantID: "ws-1", Reason: "non-payment"})
	require.NoError(t, err)
}

func TestVeridianService_Resume_EmitsEvent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Resume(ctx, "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantResumed, "ws-1", gomock.Nil()).Times(1)

	err := svc.Resume(ctx, domain.ResumeInput{TenantID: "ws-1"})
	require.NoError(t, err)
}

func TestVeridianService_SoftDelete_EmitsEvent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().SoftDelete(ctx, "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantDeleted, "ws-1", gomock.Nil()).Times(1)

	err := svc.SoftDelete(ctx, "ws-1")
	require.NoError(t, err)
}

func TestVeridianService_UpdatePlan_EmitsEventWithQuota(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-1", "business", int64(50000)).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "business", data["plan"])
			assert.Equal(t, int64(50000), data["quota"])
		}).Times(1)

	err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: "business"})
	require.NoError(t, err)
}

func TestVeridianService_UpdatePlan_RejectsEmptyInput(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "", Plan: "pro"})
	assert.ErrorContains(t, err, "tenant_id required")

	err = svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: ""})
	assert.ErrorContains(t, err, "plan required")
}

func TestVeridianService_GetStatus(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:         "ws-1",
		Plan:                "pro",
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   10000,
		EmailsSentThisMonth: 250,
	}, nil).Times(1)

	resp, err := svc.GetStatus(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, domain.PlanStatusActive, resp.Status)
	assert.Equal(t, "pro", resp.Plan)
	assert.Equal(t, int64(10000), resp.MonthlyEmailQuota)
	assert.Equal(t, int64(250), resp.EmailsSentThisMonth)
	assert.Equal(t, int64(9750), resp.QuotaRemaining)
}

func TestVeridianService_GetStatus_RejectsEmpty(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.GetStatus(context.Background(), "")
	assert.ErrorContains(t, err, "tenant_id required")
}

func TestVeridianService_GenerateMagicLink(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.user.EXPECT().GetUserByEmail(ctx, "user@example.com").
		Return(&domain.User{ID: "u-1", Email: "user@example.com"}, nil).Times(1)
	// === Veridian patch === code en clair retourne en prod ET dev (privilegied path).
	expiresAt := time.Now().Add(15 * time.Minute)
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "user@example.com", "ws-1").
		Return("code-xyz", expiresAt, nil).Times(1)

	resp, err := svc.GenerateMagicLink(ctx, "ws-1", "user@example.com")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Contains(t, resp.MagicLink, "https://notifuse.app.veridian.site/console/signin")
	assert.Contains(t, resp.MagicLink, "code=code-xyz")
	assert.Contains(t, resp.MagicLink, "email=user%40example.com")
	assert.False(t, resp.ExpiresAt.IsZero())
}

func TestVeridianService_GenerateMagicLink_RejectsEmpty(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.GenerateMagicLink(context.Background(), "", "x@y.z")
	assert.ErrorContains(t, err, "workspace_id required")

	_, err = svc.GenerateMagicLink(context.Background(), "ws-1", "")
	assert.ErrorContains(t, err, "user_email required")
}

func TestVeridianService_Suspend_PropagatesRepoError(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Suspend(ctx, "ws-1", "x").Return(errors.New("not found")).Times(1)
	// Pas d'event si erreur

	err := svc.Suspend(ctx, domain.SuspendInput{TenantID: "ws-1", Reason: "x"})
	assert.ErrorContains(t, err, "not found")
}

// === Veridian patch === AttachOwner tests
//
// AttachOwner répare un workspace existant en attachant un user humain comme
// owner. Les tests couvrent :
//   1. RejectsEmpty : validation input
//   2. AlreadyOwner : idempotence si user est déjà owner
//   3. NotAttached_TransferFromRoot : cas nominal de réparation prod
//   4. CreatesUserIfMissing : si owner_email pas dans users, on crée le user
//   5. NotAttached_NoExistingOwner : workspace orphelin → fallback root

func TestVeridianService_AttachOwner_RejectsEmpty(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.AttachOwner(context.Background(), domain.AttachOwnerInput{TenantID: "", OwnerEmail: "x@y.z"})
	assert.ErrorContains(t, err, "tenant_id required")

	_, err = svc.AttachOwner(context.Background(), domain.AttachOwnerInput{TenantID: "ws-1", OwnerEmail: ""})
	assert.ErrorContains(t, err, "owner_email required")
}

func TestVeridianService_AttachOwner_AlreadyOwner(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	humanUser := &domain.User{ID: "human-id", Email: "owner@x.test", Type: domain.UserTypeUser}

	// Step 0 : workspace existe (check 404 préliminaire).
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)

	// User humain existe déjà.
	m.user.EXPECT().GetUserByEmail(ctx, "owner@x.test").Return(humanUser, nil).Times(1)

	// État actuel : user est déjà owner du workspace → idempotent, on sort.
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "human-id", "ws-1").Return(&domain.UserWorkspace{
		UserID:      "human-id",
		WorkspaceID: "ws-1",
		Role:        "owner",
	}, nil).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-1", OwnerEmail: "owner@x.test"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Attached)
	assert.True(t, resp.AlreadyAttached)
	assert.False(t, resp.OwnerTransferred, "no transfer when already owner")
	assert.Equal(t, "human-id", resp.UserID)
}

func TestVeridianService_AttachOwner_NotAttached_TransferFromRoot(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	humanUser := &domain.User{ID: "human-id", Email: "alice@x.test", Type: domain.UserTypeUser}
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-orphan").Return(&domain.Workspace{ID: "ws-orphan"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "alice@x.test").Return(humanUser, nil).Times(1)

	// État : human pas attaché au workspace.
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "human-id", "ws-orphan").Return(nil, sql.ErrNoRows).Times(1)

	// ctxAsRoot : GetUserByEmail(root) + CreateSession + DeleteSession en defer.
	// Et 1 lookup root pour Step 7 (remove root after transfer).
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2) // root ctx + tenant ctx pour Step 7
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	// Step 4 : AddUserToWorkspace(role=member).
	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-orphan", "human-id", "member", gomock.Any(),
	).Return(nil).Times(1)

	// Step 5 : GetWorkspaceUsersWithEmail → root est owner actuel.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-orphan").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-orphan", Role: "owner"}, Email: "root@veridian.site"},
		{UserWorkspace: domain.UserWorkspace{UserID: "human-id", WorkspaceID: "ws-orphan", Role: "member"}, Email: "alice@x.test"},
	}, nil).Times(1)

	// Step 6 : TransferOwnership(workspaceID, newOwner=human, currentOwner=root).
	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-orphan", "human-id", "root-id",
	).Return(nil).Times(1)

	// Step 7 : RemoveUserFromWorkspace(root) depuis ctx tenant user.
	m.workspace.EXPECT().RemoveUserFromWorkspace(
		gomock.Any(), "ws-orphan", "root-id",
	).Return(nil).Times(1)

	// Step 8 : tenant.owner_changed event émis sur transfer réussi.
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-orphan", gomock.Any()).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-orphan", OwnerEmail: "alice@x.test"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyAttached, "human was not attached before this call")
	assert.True(t, resp.OwnerTransferred)
	assert.Equal(t, "human-id", resp.UserID)
}

func TestVeridianService_AttachOwner_CreatesUserIfMissing(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-x").Return(&domain.Workspace{ID: "ws-x"}, nil).Times(1)
	// User humain inconnu → CreateUser.
	m.user.EXPECT().GetUserByEmail(ctx, "newhuman@x.test").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil).Times(1)

	// État : pas attaché (utilise un userID généré dynamiquement, on accepte n'importe quoi).
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws-x").Return(nil, sql.ErrNoRows).Times(1)

	// ctxAsRoot pour les ops.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-x", gomock.Any(), "member", gomock.Any(),
	).Return(nil).Times(1)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-x").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-x", Role: "owner"}},
	}, nil).Times(1)

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-x", gomock.Any(), "root-id",
	).Return(nil).Times(1)

	m.workspace.EXPECT().RemoveUserFromWorkspace(
		gomock.Any(), "ws-x", "root-id",
	).Return(nil).Times(1)

	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-x", gomock.Any()).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-x", OwnerEmail: "newhuman@x.test"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Attached)
	assert.False(t, resp.AlreadyAttached)
	assert.True(t, resp.OwnerTransferred)
	assert.NotEmpty(t, resp.UserID, "user_id must be set even when user is freshly created")
}

func TestVeridianService_AttachOwner_AttachedButNotOwner_TransferOnly(t *testing.T) {
	// Cas : human est déjà member du workspace (peut-être ajouté manuellement
	// dans le passé) mais role=member, pas owner. AttachOwner doit skipper
	// le AddUserToWorkspace et juste promote via TransferOwnership.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	humanUser := &domain.User{ID: "human-id", Email: "bob@x.test", Type: domain.UserTypeUser}
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").Return(humanUser, nil).Times(1)

	// Human est déjà member (mais pas owner).
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "human-id", "ws-1").Return(&domain.UserWorkspace{
		UserID: "human-id", WorkspaceID: "ws-1", Role: "member",
	}, nil).Times(1)

	// ctxAsRoot.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	// PAS de AddUserToWorkspace : human déjà attaché.

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-1", Role: "owner"}},
		{UserWorkspace: domain.UserWorkspace{UserID: "human-id", WorkspaceID: "ws-1", Role: "member"}},
	}, nil).Times(1)

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-1", "human-id", "root-id",
	).Return(nil).Times(1)

	m.workspace.EXPECT().RemoveUserFromWorkspace(
		gomock.Any(), "ws-1", "root-id",
	).Return(nil).Times(1)

	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-1", OwnerEmail: "bob@x.test"})
	require.NoError(t, err)
	assert.True(t, resp.AlreadyAttached, "human was already a member")
	assert.True(t, resp.OwnerTransferred)
}

// ----- Health (livrable 3) -----

func TestVeridianService_Health_RejectsEmpty(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.Health(context.Background(), "")
	assert.ErrorContains(t, err, "tenant_id required")
}

func TestVeridianService_Health_TenantNotFound(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.planRepo.EXPECT().Get(ctx, "ghost").Return(nil, sql.ErrNoRows).Times(1)
	_, err := svc.Health(ctx, "ghost")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestVeridianService_Health_HealthyTenant(t *testing.T) {
	// Workspace sain : owner humain + api key + status=active → magic_link_capable=true.
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.planRepo.EXPECT().Get(ctx, "ws-h").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-h", Plan: "free", Status: domain.PlanStatusActive,
	}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-h").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "owner-id", WorkspaceID: "ws-h", Role: "owner"}, Email: "owner@x.test"},
		{UserWorkspace: domain.UserWorkspace{UserID: "key-id", WorkspaceID: "ws-h", Role: "member"}, Email: "key@x.test"},
	}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "owner-id").Return(&domain.User{ID: "owner-id", Type: domain.UserTypeUser}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "key-id").Return(&domain.User{ID: "key-id", Type: domain.UserTypeAPIKey}, nil).Times(1)

	resp, err := svc.Health(ctx, "ws-h")
	require.NoError(t, err)
	assert.Equal(t, domain.PlanStatusActive, resp.Status)
	assert.True(t, resp.OwnerAttached)
	assert.Equal(t, "owner@x.test", resp.OwnerEmail)
	assert.True(t, resp.APIKeyValid)
	assert.True(t, resp.MagicLinkCapable)
	assert.Equal(t, 2, resp.MembersCount)
	assert.Equal(t, "free", resp.Plan)
}

func TestVeridianService_Health_NoHumanOwner_DetectsBug(t *testing.T) {
	// Exactement le bug 2026-05-17 : workspace avec api_key + owner non-humain
	// (le user root historique) → owner_attached=false → magic_link_capable=false.
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.planRepo.EXPECT().Get(ctx, "ws-bug").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-bug", Plan: "free", Status: domain.PlanStatusActive,
	}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-bug").Return([]*domain.UserWorkspaceWithEmail{
		// Owner: api_key (cas du bug) — pas un user humain.
		{UserWorkspace: domain.UserWorkspace{UserID: "key-id", WorkspaceID: "ws-bug", Role: "owner"}, Email: "api@x.test"},
	}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "key-id").Return(&domain.User{ID: "key-id", Type: domain.UserTypeAPIKey}, nil).Times(1)

	resp, err := svc.Health(ctx, "ws-bug")
	require.NoError(t, err)
	assert.False(t, resp.OwnerAttached, "no human owner → bug detected")
	assert.False(t, resp.MagicLinkCapable, "without human owner the magic link flow is broken")
}

func TestVeridianService_Health_Suspended_NotCapable(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.planRepo.EXPECT().Get(ctx, "ws-susp").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-susp", Plan: "free", Status: domain.PlanStatusSuspended,
	}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-susp").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "owner-id", WorkspaceID: "ws-susp", Role: "owner"}, Email: "owner@x.test"},
		{UserWorkspace: domain.UserWorkspace{UserID: "key-id", WorkspaceID: "ws-susp", Role: "member"}, Email: "key@x.test"},
	}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "owner-id").Return(&domain.User{ID: "owner-id", Type: domain.UserTypeUser}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "key-id").Return(&domain.User{ID: "key-id", Type: domain.UserTypeAPIKey}, nil).Times(1)

	resp, err := svc.Health(ctx, "ws-susp")
	require.NoError(t, err)
	assert.True(t, resp.OwnerAttached)
	assert.False(t, resp.MagicLinkCapable, "suspended tenant must not be magic-link-capable")
	assert.Equal(t, domain.PlanStatusSuspended, resp.Status)
}

func TestVeridianService_Health_SoftDeleted(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	deletedAt := time.Now().UTC().Add(-24 * time.Hour)
	m.planRepo.EXPECT().Get(ctx, "ws-del").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-del", Plan: "free", Status: domain.PlanStatusActive, DeletedAt: &deletedAt,
	}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-del").Return([]*domain.UserWorkspaceWithEmail{}, nil).Times(1)

	resp, err := svc.Health(ctx, "ws-del")
	require.NoError(t, err)
	assert.Equal(t, domain.PlanStatusDeleted, resp.Status, "deleted_at non-nil overrides status")
	assert.False(t, resp.MagicLinkCapable)
}

// TestVeridianService_AttachOwner_TenantNotFound vérifie que AttachOwner
// renvoie sql.ErrNoRows (→ HTTP 404) quand le workspace n'existe pas.
// Sans le Step 0 (GetByID préliminaire), le code tombait sur
// GetUserWorkspace qui retourne "is not a member" → HTTP 500.
// Bug flag par l'agent Hub 2026-05-18, fixé même jour.
func TestVeridianService_AttachOwner_TenantNotFound(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.workspaceRepo.EXPECT().GetByID(ctx, "ghost-tenant").Return(nil, sql.ErrNoRows).Times(1)

	_, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ghost-tenant", OwnerEmail: "anyone@x.test"})
	require.Error(t, err)
	assert.ErrorIs(t, err, sql.ErrNoRows, "ghost tenant must return sql.ErrNoRows → 404")
}

// TestVeridianService_AttachOwner_TenantNotFoundUpstreamMessage couvre la
// variante où GetByID upstream wrap ErrNoRows dans un message ("not found",
// "no rows"). On doit aussi mapper vers ErrNoRows.
func TestVeridianService_AttachOwner_TenantNotFoundUpstreamMessage(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.workspaceRepo.EXPECT().GetByID(ctx, "ghost-2").Return(nil, errors.New("workspace not found in database")).Times(1)

	_, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ghost-2", OwnerEmail: "anyone@x.test"})
	require.Error(t, err)
	assert.ErrorIs(t, err, sql.ErrNoRows, "wrapped 'not found' must also map to 404")
}

// TestVeridianService_AttachOwner_NotAttached_UpstreamMembershipError teste
// que l'erreur upstream "user is not a member of the workspace" (vue en
// staging 2026-05-18) est bien interprétée comme "pas attaché" et non
// comme une erreur DB. Sans ce parsing, AttachOwner remontait HTTP 500
// pour tous les tenants où l'owner humain n'existait pas encore.
func TestVeridianService_AttachOwner_NotAttached_UpstreamMembershipError(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	humanUser := &domain.User{ID: "human-id", Email: "carol@x.test", Type: domain.UserTypeUser}
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-real").Return(&domain.Workspace{ID: "ws-real"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "carol@x.test").Return(humanUser, nil).Times(1)

	// Notifuse upstream renvoie cette erreur (au lieu de sql.ErrNoRows) quand
	// le user n'est pas dans user_workspaces. On doit l'accepter.
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "human-id", "ws-real").
		Return(nil, errors.New("user is not a member of the workspace")).Times(1)

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-real", "human-id", "member", gomock.Any(),
	).Return(nil).Times(1)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-real").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-real", Role: "owner"}, Email: "root@veridian.site"},
	}, nil).Times(1)

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-real", "human-id", "root-id",
	).Return(nil).Times(1)
	m.workspace.EXPECT().RemoveUserFromWorkspace(gomock.Any(), "ws-real", "root-id").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-real", gomock.Any()).Times(1)

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-real", OwnerEmail: "carol@x.test"})
	require.NoError(t, err, "upstream 'is not a member' must be parsed as not-attached, not as a DB error")
	assert.False(t, resp.AlreadyAttached)
	assert.True(t, resp.OwnerTransferred)
}

// TestVeridianService_AttachOwner_AdditiveOnlyWhenHumanOwnerExists garantit
// le comportement "additif uniquement" exigé par le README intégrations Hub :
// si un user humain est déjà owner du workspace et qu'on attache un 2e owner
// humain, le 1er ne doit PAS être retiré (cleanup root logic ne s'applique
// qu'à l'ancien owner == user root). Le 2e devient owner ; le 1er reste
// member après TransferOwnership.
func TestVeridianService_AttachOwner_AdditiveOnlyWhenHumanOwnerExists(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	newOwner := &domain.User{ID: "bob-id", Email: "bob@x.test", Type: domain.UserTypeUser}
	existingHumanOwner := &domain.User{ID: "alice-id", Email: "alice@x.test", Type: domain.UserTypeUser}
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-multi").Return(&domain.Workspace{ID: "ws-multi"}, nil).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "bob@x.test").Return(newOwner, nil).Times(1)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "bob-id", "ws-multi").Return(nil, sql.ErrNoRows).Times(1)

	// ctxAsRoot (Step 4) — 1 lookup root only. Pas de tenant session attendue
	// car l'ancien owner == alice (human, pas root) donc le cleanup root est skip.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-multi", "bob-id", "member", gomock.Any(),
	).Return(nil).Times(1)

	// alice est l'owner actuel (pas root). On transfere bob → owner, alice → member.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-multi").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "alice-id", WorkspaceID: "ws-multi", Role: "owner"}, Email: "alice@x.test"},
	}, nil).Times(1)

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-multi", "bob-id", "alice-id",
	).Return(nil).Times(1)

	// Event émis avec old_owner_email=alice (validation explicite que le
	// payload contient bien l'ancien owner, pas root).
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-multi",
		gomock.AssignableToTypeOf(map[string]interface{}{})).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "alice@x.test", data["old_owner_email"])
			assert.Equal(t, "bob@x.test", data["new_owner_email"])
		}).Times(1)

	// ⚠️ CRITICAL : pas de RemoveUserFromWorkspace appelé sur alice (additive only).
	// Si gomock voit un appel non-attendu il fait fail le test (mode strict).

	// Le cleanup root déclenché par Step 7 vérifie GetUserByEmail(root) une 2e fois
	// (sans tenant session). On l'utilise ci-dessus dans Times(2).
	_ = existingHumanOwner

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-multi", OwnerEmail: "bob@x.test"})
	require.NoError(t, err)
	assert.False(t, resp.AlreadyAttached)
	assert.True(t, resp.OwnerTransferred)
	assert.Equal(t, "bob-id", resp.UserID)
}
