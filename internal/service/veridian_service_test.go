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

	// === Veridian patch === Mark le user api_key freshly created comme
	// veridian-managed pour bloquer Team Settings -> Remove member dessus.
	m.userRepo.EXPECT().MarkVeridianManaged(ctx, "veridian-api-ws-new@ws-new.notifuse").
		Return(nil).Times(1)

	// Upsert plan
	m.planRepo.EXPECT().Upsert(ctx, gomock.AssignableToTypeOf(&domain.VeridianPlan{})).
		DoAndReturn(func(_ context.Context, p *domain.VeridianPlan) error {
			assert.Equal(t, "ws-new", p.WorkspaceID)
			assert.Equal(t, "pro", p.Plan)
			assert.Equal(t, domain.PlanStatusActive, p.Status)
			// 2026-05-20 : tous plans en quota=-1 (BYO sending — pas de limite Notifuse)
			assert.Equal(t, int64(-1), p.MonthlyEmailQuota)
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

// TestVeridianService_Provision_QuotasOverrideHardcoded verifie que
// input.Quotas.MonthlyEmails ecrase le hardcoded QuotaForPlan(plan)
// au moment du Upsert veridian_plan (CONTRAT-HUB sec. 5.17). Le test
// se base sur la meme machinerie que Provision_NewTenant mais avec
// un quota custom de 42 au lieu de 10000 pour le plan "pro".
func TestVeridianService_Provision_QuotasOverrideHardcoded(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}

	m.workspace.EXPECT().GetWorkspace(gomock.Any(), "ws-quota").Return(nil, errors.New("not found")).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-quota").Return(nil, sql.ErrNoRows).Times(1)
	m.user.EXPECT().GetUserByEmail(ctx, "owner@example.com").
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(2)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(3)
	m.workspace.EXPECT().CreateWorkspace(
		gomock.Any(), "ws-quota", "ws-quota",
		gomock.Any(), gomock.Any(), gomock.Any(),
		"UTC", gomock.Any(), "en", gomock.Any(),
	).Return(&domain.Workspace{ID: "ws-quota"}, nil).Times(1)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-quota", gomock.Any(), "member", gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws-quota", gomock.Any(), rootUser.ID).Return(nil).Times(1)
	m.workspace.EXPECT().RemoveUserFromWorkspace(gomock.Any(), "ws-quota", rootUser.ID).Return(nil).Times(1)
	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), "ws-quota", "veridian-api-ws-quota").
		Return("sk_test_apikey", "veridian-api-ws-quota@ws-quota.notifuse", nil).Times(1)
	m.userRepo.EXPECT().MarkVeridianManaged(ctx, "veridian-api-ws-quota@ws-quota.notifuse").Return(nil).Times(1)

	// Cœur du test : Upsert recoit le quota custom (42) ET le plan_source envoye.
	m.planRepo.EXPECT().Upsert(ctx, gomock.AssignableToTypeOf(&domain.VeridianPlan{})).
		DoAndReturn(func(_ context.Context, p *domain.VeridianPlan) error {
			assert.Equal(t, "ws-quota", p.WorkspaceID)
			assert.Equal(t, "pro", p.Plan)
			assert.Equal(t, int64(42), p.MonthlyEmailQuota, "quota Hub override doit ecraser le hardcoded 10000")
			assert.Equal(t, domain.PlanSourceLifetimePartner, p.PlanSource, "plan_source envoye doit etre persiste")
			return nil
		}).Times(1)

	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "owner@example.com", "ws-quota").
		Return("magic-code", time.Now().Add(15*time.Minute), nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantProvisioned, "ws-quota", gomock.Any()).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(3)

	customQuota := int64(42)
	resp, err := svc.Provision(ctx, domain.ProvisionInput{
		TenantID:   "ws-quota",
		OwnerEmail: "owner@example.com",
		Plan:       "pro",
		PlanSource: domain.PlanSourceLifetimePartner,
		Quotas:     &domain.PlanQuotasInput{MonthlyEmails: &customQuota},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-quota", resp.WorkspaceID)
}

func TestVeridianService_Provision_Idempotent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	humanOwner := &domain.User{ID: "user-1", Email: "owner@example.com", Type: domain.UserTypeUser}

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

	// === Veridian patch === Owner-check : lookup members + verifie type=user.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-existing").
		Return([]*domain.UserWorkspaceWithEmail{
			{
				UserWorkspace: domain.UserWorkspace{UserID: "user-1", WorkspaceID: "ws-existing", Role: "owner"},
				Email:         "owner@example.com",
				Type:          domain.UserTypeUser,
			},
		}, nil).Times(1)
	// lookupUserType -> userRepo.GetUserByID pour confirmer type=user.
	m.userRepo.EXPECT().GetUserByID(ctx, "user-1").Return(humanOwner, nil).Times(1)

	// === Veridian patch === Magic link FRAIS regenere dans la response idempotente.
	expiresAt := time.Now().Add(15 * time.Minute)
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "owner@example.com", "ws-existing").
		Return("fresh-magic-code", expiresAt, nil).Times(1)

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
	assert.Empty(t, resp.APIKeyEmail, "must NOT regenerate API key email on idempotent call")
	// === Veridian patch === Contrat §5.1 : magic link DOIT etre regenere.
	assert.Contains(t, resp.MagicLink, "https://notifuse.app.veridian.site/console/signin")
	assert.Contains(t, resp.MagicLink, "code=fresh-magic-code")
	assert.Contains(t, resp.MagicLink, "email=owner%40example.com")
	assert.NotEmpty(t, resp.AutoLoginURL, "auto_login_url must be regenerated on idempotent call")
}

// === Veridian patch === Idempotent + owner mismatch : refuse 409 plutot que
// silencieusement generer un magic link valide vers un workspace que le
// caller ne controle pas (contrat §5.1, ticket Hub 2026-05-18).
func TestVeridianService_Provision_OwnerMismatch_Returns409(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	realOwner := &domain.User{ID: "user-real", Email: "alice@x.test", Type: domain.UserTypeUser}

	// Lookup idempotence
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	// Workspace + plan existent
	m.workspace.EXPECT().GetWorkspace(gomock.Any(), "ws-shared").
		Return(&domain.Workspace{ID: "ws-shared"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-shared").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-shared",
		Plan:        "pro",
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)

	// Owner REEL = alice@x.test (type=user, role=owner)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-shared").
		Return([]*domain.UserWorkspaceWithEmail{
			{
				UserWorkspace: domain.UserWorkspace{UserID: "user-real", WorkspaceID: "ws-shared", Role: "owner"},
				Email:         "alice@x.test",
				Type:          domain.UserTypeUser,
			},
		}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "user-real").Return(realOwner, nil).Times(1)

	// Aucun GenerateMagicCodeForVeridian attendu — on doit fail AVANT.

	resp, err := svc.Provision(ctx, domain.ProvisionInput{
		TenantID:   "ws-shared",
		OwnerEmail: "mallory@x.test", // <- email different de l'owner reel
		Plan:       "pro",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOwnerMismatch)
	assert.Nil(t, resp)
}

// === Veridian patch === Case-insensitivity du owner match (defensive : un
// retry Hub avec un casing different ne doit pas declencher un faux 409).
func TestVeridianService_Provision_Idempotent_OwnerEmailCaseInsensitive(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	humanOwner := &domain.User{ID: "user-1", Email: "Owner@Example.COM", Type: domain.UserTypeUser}

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().GetWorkspace(gomock.Any(), "ws-case").
		Return(&domain.Workspace{ID: "ws-case"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-case").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-case",
		Plan:        "free",
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-case").
		Return([]*domain.UserWorkspaceWithEmail{
			{
				UserWorkspace: domain.UserWorkspace{UserID: "user-1", WorkspaceID: "ws-case", Role: "owner"},
				Email:         "Owner@Example.COM",
				Type:          domain.UserTypeUser,
			},
		}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "user-1").Return(humanOwner, nil).Times(1)
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, "owner@example.com", "ws-case").
		Return("code-ci", time.Now().Add(15*time.Minute), nil).Times(1)

	resp, err := svc.Provision(ctx, domain.ProvisionInput{
		TenantID:   "ws-case",
		OwnerEmail: "owner@example.com", // lowercase vs DB "Owner@Example.COM"
		Plan:       "free",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Created)
	assert.Contains(t, resp.MagicLink, "code=code-ci")
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

	m.planRepo.EXPECT().SoftDelete(ctx, "ws-1", "").Return(nil).Times(1)
	// 2 events emis : nouveau tenant.soft_deleted (sec. 5.7-5.8) + legacy
	// tenant.deleted (back-compat). Voir service.SoftDelete.
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantSoftDeleted, "ws-1", gomock.Any()).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantDeleted, "ws-1", gomock.Nil()).Times(1)

	resp, err := svc.SoftDelete(ctx, domain.SoftDeleteInput{TenantID: "ws-1"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "deleted", resp.Status)
	assert.False(t, resp.DeletedAt.IsZero())
	// purge_eligible_at = deleted_at + 30j
	assert.True(t, resp.PurgeEligibleAt.After(resp.DeletedAt))
}

// === Lifecycle (CONTRAT-HUB sec. 5.7-5.8) — Restore/Purge/Touch/UsageSummary ===

func TestVeridianService_SoftDelete_WithReason(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().SoftDelete(ctx, "ws-1", "GDPR request").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantSoftDeleted, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "GDPR request", data["reason"])
			assert.NotNil(t, data["purge_eligible_at"])
		}).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantDeleted, "ws-1", gomock.Nil()).Times(1)

	resp, err := svc.SoftDelete(ctx, domain.SoftDeleteInput{TenantID: "ws-1", Reason: "GDPR request"})
	require.NoError(t, err)
	assert.Equal(t, "deleted", resp.Status)
}

func TestVeridianService_SoftDelete_RejectsEmptyTenantID(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.SoftDelete(context.Background(), domain.SoftDeleteInput{})
	assert.ErrorContains(t, err, "tenant_id required")
}

func TestVeridianService_Restore_OK(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	deletedAt := now.Add(-7 * 24 * time.Hour)

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		DeletedAt:   &deletedAt,
	}, nil).Times(1)
	m.planRepo.EXPECT().Restore(ctx, "ws-1", "support ticket #42").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantRestored, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.Restore(ctx, domain.RestoreInput{TenantID: "ws-1", Reason: "support ticket #42"})
	require.NoError(t, err)
	assert.Equal(t, "active", resp.Status)
	assert.False(t, resp.RestoredAt.IsZero())
}

func TestVeridianService_Restore_RejectsIfNotSoftDeleted(t *testing.T) {
	// Garde-fou : on ne restore que ce qui est soft-deleted.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-active").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-active",
		DeletedAt:   nil, // pas soft-delete
	}, nil).Times(1)

	resp, err := svc.Restore(ctx, domain.RestoreInput{TenantID: "ws-active"})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrTenantNotSoftDeleted)
}

func TestVeridianService_Purge_RejectsBeforeEligibleDate(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	tomorrow := time.Now().UTC().Add(24 * time.Hour)

	m.planRepo.EXPECT().Get(ctx, "ws-vip").Return(&domain.VeridianPlan{
		WorkspaceID:     "ws-vip",
		PurgeEligibleAt: &tomorrow,
	}, nil).Times(1)

	resp, err := svc.Purge(ctx, domain.PurgeInput{
		TenantID: "ws-vip",
		Reason:   "GDPR",
		Confirm:  "PURGE",
	})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrPurgeNotEligible)
}

func TestVeridianService_Purge_RejectsIfNotSoftDeleted(t *testing.T) {
	// Tenant actif (purge_eligible_at == nil) ne peut pas etre purge.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-active").Return(&domain.VeridianPlan{
		WorkspaceID:     "ws-active",
		PurgeEligibleAt: nil,
	}, nil).Times(1)

	resp, err := svc.Purge(ctx, domain.PurgeInput{
		TenantID: "ws-active",
		Reason:   "test",
		Confirm:  "PURGE",
	})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrPurgeNotEligible)
}

func TestVeridianService_Purge_RejectsWithoutConfirm(t *testing.T) {
	// Safeguard : confirm doit valoir exactement "PURGE".
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.Purge(ctx, domain.PurgeInput{TenantID: "ws-1", Reason: "test", Confirm: "purge"})
	assert.ErrorContains(t, err, "confirm must equal")

	_, err = svc.Purge(ctx, domain.PurgeInput{TenantID: "ws-1", Reason: "test", Confirm: ""})
	assert.ErrorContains(t, err, "confirm must equal")
}

func TestVeridianService_Purge_RejectsWithoutReason(t *testing.T) {
	// GDPR : reason obligatoire.
	svc, _ := newVeridianService(t)
	_, err := svc.Purge(context.Background(), domain.PurgeInput{TenantID: "ws-1", Confirm: "PURGE"})
	assert.ErrorContains(t, err, "reason required")
}

func TestVeridianService_Touch_FreshTenantEmitsEvent(t *testing.T) {
	// Tenant jamais touche : Touch ecrit + emet l'event.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:   "ws-1",
		LastTouchedAt: nil,
	}, nil).Times(1)
	m.planRepo.EXPECT().Touch(ctx, "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantTouched, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.Touch(ctx, "ws-1")
	require.NoError(t, err)
	assert.False(t, resp.Debounced)
	assert.False(t, resp.TouchedAt.IsZero())
}

func TestVeridianService_Touch_DebouncedWithin24h(t *testing.T) {
	// Touche il y a 1h : debounce, no-op silencieux, pas d'event.
	svc, m := newVeridianService(t)
	ctx := context.Background()
	recent := time.Now().UTC().Add(-1 * time.Hour)

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:   "ws-1",
		LastTouchedAt: &recent,
	}, nil).Times(1)
	// PAS d'appel Touch ni d'Emit attendu.

	resp, err := svc.Touch(ctx, "ws-1")
	require.NoError(t, err)
	assert.True(t, resp.Debounced)
	assert.Equal(t, recent.Unix(), resp.TouchedAt.Unix(), "TouchedAt = last_touched_at preserve")
}

func TestVeridianService_Touch_ExpiredDebounceTouchesAgain(t *testing.T) {
	// Touche il y a 25h : > 24h, on touche a nouveau.
	svc, m := newVeridianService(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-25 * time.Hour)

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:   "ws-1",
		LastTouchedAt: &old,
	}, nil).Times(1)
	m.planRepo.EXPECT().Touch(ctx, "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantTouched, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.Touch(ctx, "ws-1")
	require.NoError(t, err)
	assert.False(t, resp.Debounced)
}

func TestVeridianService_UsageSummary_OK(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	lastTouch := time.Now().UTC().Add(-2 * time.Hour)

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:         "ws-1",
		Plan:                "pro",
		Status:              domain.PlanStatusActive,
		EmailsSentThisMonth: 1234,
		LastTouchedAt:       &lastTouch,
	}, nil).Times(1)

	resp, err := svc.UsageSummary(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "pro", resp.Plan)
	assert.Equal(t, domain.PlanStatusActive, resp.Status)
	assert.Equal(t, int64(1234), resp.MessagesSent30d)
	require.NotNil(t, resp.LastActivityAt)
	assert.Equal(t, lastTouch.Unix(), resp.LastActivityAt.Unix())
	assert.Equal(t, int64(0), resp.ContactsCount, "MVP : non implemente")
}

func TestVeridianService_UsageSummary_TenantNotFound(t *testing.T) {
	svc, m := newVeridianService(t)
	m.planRepo.EXPECT().Get(gomock.Any(), "ghost").Return(nil, sql.ErrNoRows).Times(1)

	resp, err := svc.UsageSummary(context.Background(), "ghost")
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestVeridianService_UpdatePlan_EmitsEventWithQuota(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
	}, nil).Times(1)
	// 2026-05-20 : business = -1 (BYO sending)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-1", "business", int64(-1), domain.PlanSource("")).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-1", gomock.Any()).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "business", data["plan"])
			assert.Equal(t, "free", data["previous_plan"])
			assert.Equal(t, int64(-1), data["quota"])
			assert.Equal(t, "stripe", data["plan_source"])
		}).Times(1)

	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: "business"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "business", resp.Plan)
	assert.Equal(t, "free", resp.PreviousPlan)
	assert.Equal(t, domain.PlanSourceStripe, resp.PlanSource)
	assert.False(t, resp.AppliedAt.IsZero())
}

func TestVeridianService_UpdatePlan_RejectsEmptyInput(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "", Plan: "pro"})
	assert.ErrorContains(t, err, "tenant_id required")

	_, err = svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: ""})
	assert.ErrorContains(t, err, "plan required")
}

func TestVeridianService_UpdatePlan_RejectsInvalidPlanSource(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID:   "ws-1",
		Plan:       "pro",
		PlanSource: domain.PlanSource("garbage"),
	})
	assert.ErrorContains(t, err, "invalid plan_source")
}

func TestVeridianService_UpdatePlan_ImmuneRejectsStripeDowngrade(t *testing.T) {
	// Cas critique sec. 3.3 : un plan offert (lifetime_partner) ne doit pas
	// pouvoir etre ecrase par un webhook Stripe.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-vip").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-vip",
		Plan:        "business",
		PlanSource:  domain.PlanSourceLifetimePartner,
	}, nil).Times(1)
	// PAS d'appel UpdatePlan attendu : on doit court-circuiter avant.

	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID:   "ws-vip",
		Plan:       "free",
		PlanSource: domain.PlanSourceStripe,
	})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrPlanImmune)
}

func TestVeridianService_UpdatePlan_ImmuneAllowsManualOverride(t *testing.T) {
	// Transition entre sources immunes (lifetime -> manual) autorisee.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-vip").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-vip",
		Plan:        "business",
		PlanSource:  domain.PlanSourceLifetimePartner,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-vip", "enterprise", int64(-1), domain.PlanSourceManual).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-vip", gomock.Any()).Times(1)

	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID:   "ws-vip",
		Plan:       "enterprise",
		PlanSource: domain.PlanSourceManual,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PlanSourceManual, resp.PlanSource)
}

func TestVeridianService_UpdatePlan_StripeToLifetimeAllowed(t *testing.T) {
	// Robert offre un plan a un user Stripe -> autorise (transition entrante).
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-stripe").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-stripe",
		Plan:        "pro",
		PlanSource:  domain.PlanSourceStripe,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-stripe", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-stripe", gomock.Any()).Times(1)

	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID:   "ws-stripe",
		Plan:       "enterprise",
		PlanSource: domain.PlanSourceLifetimePartner,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PlanSourceLifetimePartner, resp.PlanSource)
}

func TestVeridianService_UpdatePlan_EmptySourcePreservesExisting(t *testing.T) {
	// Sec compat : appel Hub legacy sans plan_source ne doit pas changer la
	// source existante (cf. repo COALESCE). On verifie le comportement du
	// service via la response : effectiveSource = existing.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-vip").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-vip",
		Plan:        "business",
		PlanSource:  domain.PlanSourceLifetimePartner,
	}, nil).Times(1)
	// PlanSource passe = "" -> repo COALESCE preserve la valeur existante.
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-vip", "enterprise", int64(-1), domain.PlanSource("")).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-vip", gomock.Any()).Times(1)

	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID: "ws-vip",
		Plan:     "enterprise",
		// PlanSource laisse vide
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PlanSourceLifetimePartner, resp.PlanSource, "doit refleter l'existant preserve, pas 'stripe'")
}

func TestVeridianService_UpdatePlan_QuotasOverrideHardcoded(t *testing.T) {
	// CONTRAT-HUB sec. 5.17 : si le Hub envoie input.Quotas.MonthlyEmails,
	// utiliser cette valeur plutot que le hardcoded QuotaForPlan(plan).
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
	}, nil).Times(1)
	// Le quota envoyé (999) doit etre passe au repo, pas le hardcoded 10000 pour "pro".
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-1", "pro", int64(999), domain.PlanSource("")).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)

	customQuota := int64(999)
	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID: "ws-1",
		Plan:     "pro",
		Quotas:   &domain.PlanQuotasInput{MonthlyEmails: &customQuota},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestVeridianService_UpdatePlan_QuotasNilFallsBackToHardcoded(t *testing.T) {
	// Si input.Quotas est nil, on utilise QuotaForPlan(plan).
	// 2026-05-20 : tous plans = -1 (BYO sending).
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-1", "pro", int64(-1), domain.PlanSource("")).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)

	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: "pro"})
	require.NoError(t, err)
}

func TestVeridianService_UpdatePlan_QuotasMonthlyEmailsNilFallsBack(t *testing.T) {
	// Cas particulier : input.Quotas non-nil mais MonthlyEmails nil (le Hub
	// envoie une struct vide en preparation de futurs quotas) → fallback hardcoded.
	// 2026-05-20 : tous plans = -1 (BYO sending).
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-1", "pro", int64(-1), domain.PlanSource("")).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)

	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID: "ws-1",
		Plan:     "pro",
		Quotas:   &domain.PlanQuotasInput{}, // struct vide
	})
	require.NoError(t, err)
}

func TestVeridianService_UpdatePlan_QuotasMonthlyEmailsUnlimited(t *testing.T) {
	// Cas illimite : Hub envoie -1 explicitement.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-vip").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-vip",
		Plan:        "business",
		PlanSource:  domain.PlanSourceLifetimePartner,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-vip", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1)

	unlimited := int64(-1)
	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{
		TenantID:   "ws-vip",
		Plan:       "enterprise",
		PlanSource: domain.PlanSourceLifetimePartner,
		Quotas:     &domain.PlanQuotasInput{MonthlyEmails: &unlimited},
	})
	require.NoError(t, err)
}

func TestVeridianService_UpdatePlan_NotFoundProsRepo(t *testing.T) {
	// Si le Get retourne sql.ErrNoRows, le service propage tel quel.
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ghost").Return(nil, sql.ErrNoRows).Times(1)

	resp, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ghost", Plan: "pro"})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestVeridianService_GetStatus(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// 2026-05-20 : tous plans = -1 (BYO sending → quota_remaining = -1 unlimited).
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:         "ws-1",
		Plan:                "pro",
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   -1,
		EmailsSentThisMonth: 250,
	}, nil).Times(1)

	resp, err := svc.GetStatus(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, domain.PlanStatusActive, resp.Status)
	assert.Equal(t, "pro", resp.Plan)
	assert.Equal(t, int64(-1), resp.MonthlyEmailQuota)
	assert.Equal(t, int64(250), resp.EmailsSentThisMonth) // compteur conserve pour stats
	assert.Equal(t, int64(-1), resp.QuotaRemaining)       // unlimited
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

	// Step 3 : list members pour identifier owner courant (root ici).
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-orphan").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-orphan", Role: "owner"}, Email: "root@veridian.site"},
	}, nil).Times(1)

	// Step 4 : ctxAsUser(currentOwner=root) — 1 CreateSession/DeleteSession.
	// Step 7 : ctxAsUser(new owner=human) pour remove root — 1 second pair.
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	// Step 5 : AddUserToWorkspace via callerCtx (root, current owner).
	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-orphan", "human-id", "member", gomock.Any(),
	).Return(nil).Times(1)

	// Step 6 : TransferOwnership(workspaceID, newOwner=human, currentOwner=root).
	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-orphan", "human-id", "root-id",
	).Return(nil).Times(1)

	// Step 7 : check si currentOwner == root → GetUserByEmail(root) 1×.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)

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

	// Step 3 : list members → root est owner courant.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-x").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-x", Role: "owner"}},
	}, nil).Times(1)

	// Step 4 + Step 7 : 2 paires session (callerCtx=root + tenantCtx=human).
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	// Step 7 only : GetUserByEmail(root) pour check si current owner == root.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)

	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-x", gomock.Any(), "member", gomock.Any(),
	).Return(nil).Times(1)

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

	// Step 3 : list members → root est owner courant.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-1").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-1", Role: "owner"}},
		{UserWorkspace: domain.UserWorkspace{UserID: "human-id", WorkspaceID: "ws-1", Role: "member"}},
	}, nil).Times(1)

	// Step 4 + Step 7 sessions (callerCtx=root + tenantCtx=human).
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	// PAS de AddUserToWorkspace : human déjà attaché.

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-1", "human-id", "root-id",
	).Return(nil).Times(1)

	// Step 7 : GetUserByEmail(root) pour check si current owner == root.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)

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
	// 404 réservé au cas "workspace absent" depuis 2026-05-18.
	// Plan absent seul = legacy workspace, pas une 404 (cf TestVeridianService_Health_LegacyWorkspaceWithoutPlan).
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.workspaceRepo.EXPECT().GetByID(ctx, "ghost").Return(nil, sql.ErrNoRows).Times(1)
	_, err := svc.Health(ctx, "ghost")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// TestVeridianService_Health_LegacyWorkspaceWithoutPlan couvre le bug 2026-05-18 :
// 9 workspaces prod existent dans `workspaces` mais n'ont pas de row dans
// `veridian_plan` (créés avant la table). Avant le fix, Health renvoyait 404
// alors que le tenant est fonctionnel. Maintenant : 200 avec plan/status vides.
func TestVeridianService_Health_LegacyWorkspaceWithoutPlan(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-legacy").Return(&domain.Workspace{ID: "ws-legacy"}, nil).Times(1)
	m.planRepo.EXPECT().Get(ctx, "ws-legacy").Return(nil, sql.ErrNoRows).Times(1)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-legacy").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "owner-id", WorkspaceID: "ws-legacy", Role: "owner"}, Email: "owner@x.test"},
		{UserWorkspace: domain.UserWorkspace{UserID: "key-id", WorkspaceID: "ws-legacy", Role: "member"}, Email: "key@x.test"},
	}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "owner-id").Return(&domain.User{ID: "owner-id", Type: domain.UserTypeUser}, nil).Times(1)
	m.userRepo.EXPECT().GetUserByID(ctx, "key-id").Return(&domain.User{ID: "key-id", Type: domain.UserTypeAPIKey}, nil).Times(1)

	resp, err := svc.Health(ctx, "ws-legacy")
	require.NoError(t, err, "legacy workspace must NOT return 404")
	assert.Equal(t, "", resp.Plan, "plan vide → Hub interprète comme legacy à enroller")
	assert.Equal(t, domain.PlanStatus(""), resp.Status, "status vide quand plan absent")
	assert.True(t, resp.OwnerAttached, "owner humain présent → attached")
	assert.True(t, resp.APIKeyValid, "api key présente → valid")
	// Legacy workspace : owner humain + api key présents → magic link OK
	// même sans plan. C'est l'invariant business validé pour les 9 tenants prod
	// (cf ticket todo/2026-05-18-health-404-sur-workspace-sans-plan.md) :
	// le flow user marchait DÉJÀ malgré le 404 fantôme, il continue de marcher.
	assert.True(t, resp.MagicLinkCapable, "legacy workspace avec owner + api key reste magic-link-capable")
	assert.Equal(t, 2, resp.MembersCount)
}

func TestVeridianService_Health_HealthyTenant(t *testing.T) {
	// Workspace sain : owner humain + api key + status=active → magic_link_capable=true.
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-h").Return(&domain.Workspace{ID: "ws-h"}, nil).Times(1)
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
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-bug").Return(&domain.Workspace{ID: "ws-bug"}, nil).Times(1)
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
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-susp").Return(&domain.Workspace{ID: "ws-susp"}, nil).Times(1)
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
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws-del").Return(&domain.Workspace{ID: "ws-del"}, nil).Times(1)
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

	// Step 3 : list members → root est owner courant.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-real").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "root-id", WorkspaceID: "ws-real", Role: "owner"}, Email: "root@veridian.site"},
	}, nil).Times(1)

	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-real", "human-id", "member", gomock.Any(),
	).Return(nil).Times(1)

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-real", "human-id", "root-id",
	).Return(nil).Times(1)

	// Step 7 : GetUserByEmail(root) pour check si current owner == root.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)
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

	// Step 3 : alice est l'owner actuel (pas root). On transfere bob → owner.
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws-multi").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "alice-id", WorkspaceID: "ws-multi", Role: "owner"}, Email: "alice@x.test"},
	}, nil).Times(1)

	// Step 4 : ctxAsUser(alice) — 1 paire CreateSession/DeleteSession.
	// Pas de tenant session pour Step 7 car alice est human (pas root).
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	m.workspace.EXPECT().AddUserToWorkspace(
		gomock.Any(), "ws-multi", "bob-id", "member", gomock.Any(),
	).Return(nil).Times(1)

	m.workspace.EXPECT().TransferOwnership(
		gomock.Any(), "ws-multi", "bob-id", "alice-id",
	).Return(nil).Times(1)

	// Step 7 : GetUserByEmail(root) appelé pour comparer current owner.
	// alice-id != root-id donc cleanup root skip → pas de RemoveUserFromWorkspace.
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(1)

	// Event émis avec old_owner_email=alice (validation explicite que le
	// payload contient bien l'ancien owner, pas root).
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantOwnerChanged, "ws-multi",
		gomock.AssignableToTypeOf(map[string]interface{}{})).
		Do(func(_ context.Context, _ domain.VeridianEvent, _ string, data map[string]interface{}) {
			assert.Equal(t, "alice@x.test", data["old_owner_email"])
			assert.Equal(t, "bob@x.test", data["new_owner_email"])
		}).Times(1)

	// ⚠️ CRITICAL : pas de RemoveUserFromWorkspace appelé sur alice (additive only).
	_ = existingHumanOwner

	resp, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws-multi", OwnerEmail: "bob@x.test"})
	require.NoError(t, err)
	assert.False(t, resp.AlreadyAttached)
	assert.True(t, resp.OwnerTransferred)
	assert.Equal(t, "bob-id", resp.UserID)
}

// === Veridian patch 2026-05-20 — e2e-cleanup-discipline-canary-safety ===
//
// defaultSafetyClientPrefixes est la dernière ligne de défense contre un wipe
// accidentel d'un tenant légitime (client réel, canary witness, workspace
// personnel de Robert). Toute régression sur cette slice = risque de
// suppression irréversible d'un tenant prod.
//
// Ce test vérifie 3 invariants :
//   1. Tous les prefixes connus sont déclarés (clients réels + canary + Robert).
//   2. Aucune entrée doublon (qui serait du bruit).
//   3. Aucune entrée trop courte (< 3 chars) qui ferait match trop large
//      (ex: "rb" matcherait "rbrunon" mais aussi "rbtest123").
func TestVeridianService_DefaultSafetyClientPrefixes_ContainsCriticalEntries(t *testing.T) {
	required := []string{
		// Clients réels staging + prod
		"apicalinfo", "robinix", "lyon", "loyer", "veridiansite",
		"antjacquet", "darysisowath", "guilhemjacquet", "ismailelmouaddab",
		// Canary witness tenants (long-lived, utilisés pour détecter régressions)
		"canary",
		// Workspaces personnels Robert
		"robertbrunon", "robertstagingtest", "brunon5robert", "rbrunon", "truy",
	}

	have := make(map[string]int, len(defaultSafetyClientPrefixes))
	for _, p := range defaultSafetyClientPrefixes {
		have[p]++
		assert.GreaterOrEqual(t, len(p), 3,
			"prefix %q trop court (< 3 chars) → risque de match trop large", p)
	}

	for _, r := range required {
		assert.GreaterOrEqual(t, have[r], 1,
			"prefix %q manquant dans defaultSafetyClientPrefixes (tenant risque d'être wipé)", r)
	}

	for p, count := range have {
		assert.Equal(t, 1, count, "prefix %q est dupliqué (count=%d)", p, count)
	}
}

// Vérifie que WipeTestTenants skip bien les tenants matchant la prefix-list
// par défaut. Test fonctionnel : on passe explicitement TenantIDs pour
// éviter de mocker planRepo.ListByPrefix (le but est de tester le filtre
// safety, pas la resolution prefix → ids).
//
// Comportement attendu : tous les tenants matchant un safety prefix
// atterrissent dans resp.Skipped, aucun dans resp.Wiped, aucune erreur.
// Aucun appel à wipeOneTenant ne doit avoir lieu (pas d'EXPECT sur
// workspace.DeleteWorkspace ou planRepo.HardDelete).
func TestVeridianService_WipeTestTenants_SkipsCanaryAndClientPrefixes(t *testing.T) {
	svc, m := newVeridianService(t)

	// Setup minimal ctxAsRoot mock (called once at the start of WipeTestTenants).
	// ctxAsRoot fait : GetUserByEmail(rootEmail) → CreateSession → defer DeleteSession.
	// On stub les 3 méthodes du userRepo, c'est suffisant — pas de wipe réel
	// déclenché car tous les tids matchent un safety prefix.
	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").
		Return(rootUser, nil).AnyTimes()
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	candidates := []string{
		"canaryfree",          // canary witness → MUST skip
		"canarypro",           // canary witness → MUST skip
		"canaryenterprise",    // canary witness → MUST skip
		"robertbrunon42",      // Robert perso → MUST skip
		"apicalinfoclient1",   // client réel → MUST skip
		"antjacquet-staging",  // client réel → MUST skip
	}

	resp, err := svc.WipeTestTenants(context.Background(), domain.WipeTestTenantsInput{
		TenantIDs: candidates,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.ElementsMatch(t, candidates, resp.Skipped,
		"tous les tenants matchant un safety prefix doivent être skippés")
	assert.Empty(t, resp.Wiped, "aucun tenant safety ne doit avoir été wipé")
	assert.Empty(t, resp.Errors, "aucune erreur attendue, juste des skips")
}

// === V37 — GetLimits (lot 3 pricing-plans) ===

func TestVeridianService_GetLimits_RejectsEmpty(t *testing.T) {
	svc, _ := newVeridianService(t)
	_, err := svc.GetLimits(context.Background(), "")
	assert.ErrorContains(t, err, "tenant_id required")
}

func TestVeridianService_GetLimits_NotFound(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ghost").Return(nil, sql.ErrNoRows).Times(1)

	resp, err := svc.GetLimits(ctx, "ghost")
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, sql.ErrNoRows, "sql.ErrNoRows propage pour mapping 404 handler")
}

func TestVeridianService_GetLimits_ReadsV37Dimensions(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Tenant pro post-pivot 2026-05-21 : tout illimite.
	m.planRepo.EXPECT().Get(ctx, "ws-pro").Return(&domain.VeridianPlan{
		WorkspaceID:            "ws-pro",
		Plan:                   "pro",
		PlanSource:             domain.PlanSourceStripe,
		Status:                 domain.PlanStatusActive,
		MonthlyEmailQuota:      -1,
		MaxContacts:            -1,
		MaxSeats:               -1,
		MaxOAuthAccounts:       -1,
		MaxCustomDomains:       -1,
		MaxActiveSequences:     -1,
		FeatureABTesting:       true,
		FeatureBrandingRemoved: true,
		FeatureWhiteLabel:      false,
		HistoryRetentionDays:   -1,
	}, nil).Times(1)

	resp, err := svc.GetLimits(ctx, "ws-pro")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "ws-pro", resp.TenantID)
	assert.Equal(t, "pro", resp.Plan)
	assert.Equal(t, domain.PlanSourceStripe, resp.PlanSource)
	assert.Equal(t, domain.PlanStatusActive, resp.Status)
	assert.Equal(t, int64(-1), resp.Limits.MaxContacts)
	assert.Equal(t, -1, resp.Limits.MaxSeats)
	assert.Equal(t, -1, resp.Limits.MaxActiveSequences)
	assert.True(t, resp.Limits.FeatureABTesting)
	assert.True(t, resp.Limits.FeatureBrandingRemoved)
	assert.False(t, resp.Limits.FeatureWhiteLabel, "Pro != Business (white-label seul differenciant)")
	assert.Equal(t, -1, resp.Limits.HistoryRetentionDays)
	assert.False(t, resp.GeneratedAt.IsZero(), "GeneratedAt set pour cache TTL caller")
}

// TestVeridianService_GetLimits_LegacyZeroFallsBackToPlanDefaults verifie
// le fallback safe : row antedeluvien dont toutes les dimensions V37 sont
// a zero (cas tenant cree pre-V37 ou backfill rate sur ce tenant) — on
// retombe sur LimitsForPlan(p.Plan) au lieu d'exposer des 0 absurdes.
func TestVeridianService_GetLimits_LegacyZeroFallsBackToPlanDefaults(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Row "antedeluvien" : Plan = pro mais TOUTES les dimensions a zero.
	// Cas reel : tenant cree avant V37 et jamais re-upsert depuis.
	m.planRepo.EXPECT().Get(ctx, "ws-legacy").Return(&domain.VeridianPlan{
		WorkspaceID:       "ws-legacy",
		Plan:              "pro",
		PlanSource:        domain.PlanSourceStripe,
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: -1,
		// Toutes les dimensions V37 implicitement a zero/false
	}, nil).Times(1)

	resp, err := svc.GetLimits(ctx, "ws-legacy")
	require.NoError(t, err)
	require.NotNil(t, resp)
	// Fallback applique : pivot 2026-05-21, Pro = tout illimite.
	assert.Equal(t, int64(-1), resp.Limits.MaxContacts, "fallback Pro post-pivot = illimite")
	assert.Equal(t, -1, resp.Limits.MaxSeats)
	assert.True(t, resp.Limits.FeatureABTesting, "fallback Pro = A/B testing on")
}

// TestVeridianService_GetLimits_UnknownPlanFallsBackToFree — un row avec
// plan inexistant doit retomber sur Free strict via LimitsForPlan.
// Post-pivot 2026-05-21 : Free = tout illimite aussi, donc fallback safe.
func TestVeridianService_GetLimits_UnknownPlanFallsBackToFree(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ws-mystery").Return(&domain.VeridianPlan{
		WorkspaceID:       "ws-mystery",
		Plan:              "tier-from-the-future",
		PlanSource:        domain.PlanSourceManual,
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: -1,
		// Dimensions V37 a zero → trigger fallback → Plan inconnu → Free
	}, nil).Times(1)

	resp, err := svc.GetLimits(ctx, "ws-mystery")
	require.NoError(t, err)
	assert.Equal(t, "tier-from-the-future", resp.Plan, "plan preserve dans la reponse")
	// Fallback Free post-pivot = tout illimite y compris pour plan inconnu.
	// La SEULE chose qui distingue Free vs paid = white-label (false) +
	// la deadline temps geree cote Hub.
	assert.Equal(t, int64(-1), resp.Limits.MaxContacts)
	assert.Equal(t, -1, resp.Limits.MaxSeats)
	assert.True(t, resp.Limits.FeatureBrandingRemoved, "pivot : branding optionnel meme pour Free")
	assert.False(t, resp.Limits.FeatureWhiteLabel, "Free n'a PAS white-label custom (Business+ only)")
}

// TestLookupByEmail_ServiceExposed : compile-time check que LookupByEmail est
// bien dans le contrat veridianService (Constitution §1 mapping 1-pour-1).
// Le contenu fonctionnel est couvert par veridian_discovery_service_test.go.
func TestLookupByEmail_ServiceExposed(t *testing.T) {
	// Verify the service implements LookupByEmail via the domain.VeridianService interface.
	// If LookupByEmail is removed from the interface or service, this test file won't compile.
	var _ func(svc *veridianService) interface{} = func(svc *veridianService) interface{} {
		return svc.LookupByEmail
	}
	assert.True(t, true, "compile-time check passed: LookupByEmail exists on veridianService")
}

// TestAttachMember_ServiceExposed : compile-time check que AttachMember est
// bien dans le contrat veridianService (Constitution §1 mapping 1-pour-1).
// Le contenu fonctionnel est couvert par veridian_attach_member_service_test.go.
func TestAttachMember_ServiceExposed(t *testing.T) {
	var _ func(svc *veridianService) interface{} = func(svc *veridianService) interface{} {
		return svc.AttachMember
	}
	assert.True(t, true, "compile-time check passed: AttachMember exists on veridianService")
}
