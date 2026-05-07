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
	workspace *mocks.MockWorkspaceServiceInterface
	user      *mocks.MockUserServiceInterface
	userRepo  *mocks.MockUserRepository
	planRepo  *mocks.MockVeridianPlanRepository
	emitter   *mocks.MockWebhookEmitter
}

func newVeridianService(t *testing.T) (*veridianService, *veridianServiceMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	m := &veridianServiceMocks{
		workspace: mocks.NewMockWorkspaceServiceInterface(ctrl),
		user:      mocks.NewMockUserServiceInterface(ctrl),
		userRepo:  mocks.NewMockUserRepository(ctrl),
		planRepo:  mocks.NewMockVeridianPlanRepository(ctrl),
		emitter:   mocks.NewMockWebhookEmitter(ctrl),
	}

	svc := &veridianService{
		workspaceService: m.workspace,
		userService:      m.user,
		userRepo:         m.userRepo,
		planRepo:         m.planRepo,
		emitter:          m.emitter,
		defaultPlan:      "free",
		rootEmail:        "root@veridian.site",
		apiEndpoint:      "https://notifuse.app.veridian.site",
		logger:           logger.NewLogger(),
	}
	return svc, m
}

func TestVeridianService_New_DefaultsPlanToFree(t *testing.T) {
	svc := NewVeridianService(nil, nil, nil, nil, nil, "", "root@x", "http://x", logger.NewLogger())
	require.NotNil(t, svc)
	concrete := svc.(*veridianService)
	assert.Equal(t, "free", concrete.defaultPlan)
}

func TestVeridianService_Provision_NewTenant(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	// Tenant inexistant : workspace + plan absents
	m.workspace.EXPECT().GetWorkspace(ctx, "ws-new").Return(nil, errors.New("not found")).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-new").Return(nil, sql.ErrNoRows).Times(1)

	// Owner pas trouve par UserService
	m.user.EXPECT().GetUserByEmail(ctx, "owner@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)

	// Creation owner user
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil).Times(1)

	// ctxAsRoot : lookup root user + create session
	m.userRepo.EXPECT().GetUserByEmail(ctx, "root@veridian.site").Return(rootUser, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(ctx, gomock.Any()).Return(nil).Times(1)

	// Workspace creation
	m.workspace.EXPECT().CreateWorkspace(
		gomock.Any(), "ws-new", "ws-new",
		gomock.Any(), gomock.Any(), gomock.Any(),
		"UTC", gomock.Any(), "en", gomock.Any(),
	).Return(&domain.Workspace{ID: "ws-new"}, nil).Times(1)

	// AddUserToWorkspace owner
	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-new", gomock.Any(), "owner", gomock.Any(),
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

	// Cleanup session
	m.userRepo.EXPECT().DeleteSession(ctx, gomock.Any()).Return(nil).Times(1)

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

	// Tenant existe deja : workspace + plan presents
	m.workspace.EXPECT().GetWorkspace(ctx, "ws-existing").
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
