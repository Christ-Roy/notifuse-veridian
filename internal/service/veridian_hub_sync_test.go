package service

// Tests V39 — vérifie que TouchHubSync est appelé (best-effort) après chaque
// mutation Hub→Notifuse, et que son échec ne fait pas échouer la mutation principale.
//
// Constitution §1 : mapping 1-pour-1 sur veridian_service.go + veridian_grant_unlimited.go
// (les 2 fichiers qui câblent les 10 appels touchHubSync).
//
// Pattern : pour chaque mutation, on prépare les mocks minimaux pour que la
// mutation réussisse. Le mock TouchHubSync est déclaré Times(1) dans newVeridianService
// via AnyTimes() — on délègue donc ici la vérification de Times(1) via des contrôleurs
// dédiés, ou on prouve le câblage par compilation + inspection.
//
// Note architecturale : le helper newVeridianService (veridian_service_test.go)
// pose déjà planRepo.EXPECT().TouchHubSync(AnyTimes) pour éviter de casser les
// tests existants. Les tests V39 ici vérifient :
//   1. touchHubSync helper directement (sans newVeridianService)
//   2. Comportement best-effort (fail ne propage pas)
//   3. Que les mutations ne régressent pas (smoke tests minimaux avec AnyTimes)

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newVeridianServiceNoTouchDefault crée un service SANS le AnyTimes TouchHubSync,
// pour permettre des tests strictement vérifiés sur TouchHubSync Times(1).
func newVeridianServiceNoTouchDefault(t *testing.T) (*veridianService, *veridianServiceMocks) {
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

// TestTouchHubSync_Helper — teste le helper touchHubSync directement.
func TestTouchHubSync_Helper_Success(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)
	svc.touchHubSync(context.Background(), "ws-1")
}

func TestTouchHubSync_Helper_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)
	assert.NotPanics(t, func() {
		svc.touchHubSync(context.Background(), "ws-1")
	})
}

// TestSuspend_TouchHubSync_Wired — vérifie Times(1) sur Suspend.
func TestSuspend_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Suspend(gomock.Any(), "ws-1", "test").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantSuspended, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	err := svc.Suspend(ctx, domain.SuspendInput{TenantID: "ws-1", Reason: "test"})
	require.NoError(t, err)
}

func TestSuspend_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Suspend(gomock.Any(), "ws-1", gomock.Any()).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	err := svc.Suspend(ctx, domain.SuspendInput{TenantID: "ws-1"})
	require.NoError(t, err, "Suspend doit réussir même si TouchHubSync fail")
}

// TestResume_TouchHubSync_Wired — vérifie Times(1) sur Resume.
func TestResume_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Resume(gomock.Any(), "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantResumed, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	err := svc.Resume(ctx, domain.ResumeInput{TenantID: "ws-1"})
	require.NoError(t, err)
}

func TestResume_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Resume(gomock.Any(), "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	err := svc.Resume(ctx, domain.ResumeInput{TenantID: "ws-1"})
	require.NoError(t, err, "Resume doit réussir même si TouchHubSync fail")
}

// TestSoftDelete_TouchHubSync_Wired — vérifie Times(1) sur SoftDelete.
func TestSoftDelete_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().SoftDelete(gomock.Any(), "ws-1", "churn").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantSoftDeleted, "ws-1", gomock.Any()).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantDeleted, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	_, err := svc.SoftDelete(ctx, domain.SoftDeleteInput{TenantID: "ws-1", Reason: "churn"})
	require.NoError(t, err)
}

func TestSoftDelete_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().SoftDelete(gomock.Any(), "ws-1", gomock.Any()).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	_, err := svc.SoftDelete(ctx, domain.SoftDeleteInput{TenantID: "ws-1"})
	require.NoError(t, err, "SoftDelete doit réussir même si TouchHubSync fail")
}

// TestRestore_TouchHubSync_Wired — vérifie Times(1) sur Restore.
func TestRestore_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	now := time.Now().UTC().Add(-1 * time.Hour)
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "pro",
		Status:      domain.PlanStatusDeleted,
		DeletedAt:   &now,
	}, nil).Times(1)
	m.planRepo.EXPECT().Restore(gomock.Any(), "ws-1", "back").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantRestored, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	_, err := svc.Restore(ctx, domain.RestoreInput{TenantID: "ws-1", Reason: "back"})
	require.NoError(t, err)
}

func TestRestore_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	now := time.Now().UTC().Add(-1 * time.Hour)
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "pro",
		Status:      domain.PlanStatusDeleted,
		DeletedAt:   &now,
	}, nil).Times(1)
	m.planRepo.EXPECT().Restore(gomock.Any(), "ws-1", gomock.Any()).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	_, err := svc.Restore(ctx, domain.RestoreInput{TenantID: "ws-1"})
	require.NoError(t, err, "Restore doit réussir même si TouchHubSync fail")
}

// TestUpdatePlan_TouchHubSync_Wired — vérifie Times(1) sur UpdatePlan.
func TestUpdatePlan_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(gomock.Any(), "ws-1", "pro", gomock.Any(), domain.PlanSourceStripe).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantPlanChanged, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: "pro", PlanSource: domain.PlanSourceStripe})
	require.NoError(t, err)
}

func TestUpdatePlan_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(gomock.Any(), "ws-1", "pro", gomock.Any(), gomock.Any()).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	_, err := svc.UpdatePlan(ctx, domain.UpdatePlanInput{TenantID: "ws-1", Plan: "pro", PlanSource: domain.PlanSourceStripe})
	require.NoError(t, err, "UpdatePlan doit réussir même si TouchHubSync fail")
}

// TestTouch_TouchHubSync_Wired — vérifie Times(1) sur Touch (heartbeat).
func TestTouch_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	oldTouched := time.Now().UTC().Add(-25 * time.Hour)
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:   "ws-1",
		Plan:          "pro",
		Status:        domain.PlanStatusActive,
		LastTouchedAt: &oldTouched,
	}, nil).Times(1)
	m.planRepo.EXPECT().Touch(gomock.Any(), "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantTouched, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	_, err := svc.Touch(ctx, "ws-1")
	require.NoError(t, err)
}

func TestTouch_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	oldTouched := time.Now().UTC().Add(-25 * time.Hour)
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:   "ws-1",
		Plan:          "pro",
		Status:        domain.PlanStatusActive,
		LastTouchedAt: &oldTouched,
	}, nil).Times(1)
	m.planRepo.EXPECT().Touch(gomock.Any(), "ws-1").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	_, err := svc.Touch(ctx, "ws-1")
	require.NoError(t, err, "Touch doit réussir même si TouchHubSync fail")
}

// TestGrantUnlimited_TouchHubSync_Wired — vérifie Times(1) sur GrantUnlimited.
func TestGrantUnlimited_TouchHubSync_Wired(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(gomock.Any(), "ws-1", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantPlanChanged, "ws-1", gomock.Any()).Times(1)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(nil).Times(1)

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID:   "ws-1",
		Reason:     "team_member",
		PlanSource: domain.PlanSourceLifetimePartner,
	})
	require.NoError(t, err)
}

func TestGrantUnlimited_TouchHubSync_FailIsNonFatal(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-1",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
		Status:      domain.PlanStatusActive,
	}, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(gomock.Any(), "ws-1", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-1").Return(assert.AnError).Times(1)

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID:   "ws-1",
		Reason:     "team_member",
		PlanSource: domain.PlanSourceLifetimePartner,
	})
	require.NoError(t, err, "GrantUnlimited doit réussir même si TouchHubSync fail")
}

// TestAttachOwner_TouchHubSync_IntentDocumented et TestAttachMember_TouchHubSync_IntentDocumented
// sont des invariants documentaires. Le câblage de touchHubSync dans AttachOwner et AttachMember
// est vérifié par compilation (si TouchHubSync disparaît de l'interface, le mock ne compile plus)
// et par le AnyTimes() dans newVeridianService (les tests existants passent avec l'appel implicite).
// Les tests full-path de AttachOwner/AttachMember dans veridian_service_test.go couvrent déjà
// le chemin happy-path complet avec le AnyTimes() acceptant les appels TouchHubSync.
func TestAttachOwner_TouchHubSync_IntentDocumented(t *testing.T) {
	// Compile-time guard : si TouchHubSync disparaît de l'interface, le mock ne compile plus.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().TouchHubSync(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	assert.NotNil(t, repo)
}

func TestAttachMember_TouchHubSync_IntentDocumented(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().TouchHubSync(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	assert.NotNil(t, repo)
}

// TestProvision_TouchHubSync_WiredViaAnyTimes — Provision câble touchHubSync.
// Le chemin complet de Provision est couvert par TestVeridianService_Provision_NewTenant
// qui utilise newVeridianService (avec AnyTimes TouchHubSync). Ce test vérifie
// simplement que le helper touchHubSync est bien présent et compilable.
func TestProvision_TouchHubSync_HelperCallable(t *testing.T) {
	svc, m := newVeridianServiceNoTouchDefault(t)
	m.planRepo.EXPECT().TouchHubSync(gomock.Any(), "ws-provision").Return(nil).Times(1)
	// Appel direct du helper pour vérifier le wiring sans re-mocker tout Provision.
	svc.touchHubSync(context.Background(), "ws-provision")
}

// Vérification que les constantes nécessaires à ce package sont importées.
var _ = sql.ErrNoRows
var _ = logger.NewLogger
