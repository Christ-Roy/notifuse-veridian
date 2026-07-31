package service

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testProvisionLockingPlanRepo reproduit uniquement la semantique du verrou
// Postgres pour les tests service. Les tests repository verifient separement
// que l'implementation runtime utilise bien pg_advisory_xact_lock.
type testProvisionLockingPlanRepo struct {
	domain.VeridianPlanRepository

	mu         sync.Mutex
	locks      map[string]chan struct{}
	acquireErr error
}

func newTestProvisionLockingPlanRepo(repo domain.VeridianPlanRepository) *testProvisionLockingPlanRepo {
	return &testProvisionLockingPlanRepo{
		VeridianPlanRepository: repo,
		locks:                  make(map[string]chan struct{}),
	}
}

func (r *testProvisionLockingPlanRepo) AcquireProvisionLock(ctx context.Context, tenantID string) (func(context.Context) error, error) {
	if r.acquireErr != nil {
		return nil, r.acquireErr
	}
	r.mu.Lock()
	lock := r.locks[tenantID]
	if lock == nil {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		r.locks[tenantID] = lock
	}
	r.mu.Unlock()

	select {
	case <-lock:
		var once sync.Once
		return func(context.Context) error {
			once.Do(func() { lock <- struct{}{} })
			return nil
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type testPlanRepoWithoutProvisionLock struct {
	domain.VeridianPlanRepository
}

func TestVeridianService_AcquireProvisionLock_FailsClosedWithoutDatabaseCapability(t *testing.T) {
	svc, m := newVeridianService(t)
	svc.planRepo = &testPlanRepoWithoutProvisionLock{VeridianPlanRepository: m.planRepo}

	resp, err := svc.Provision(t.Context(), domain.ProvisionInput{
		TenantID:   "ws-no-lock",
		OwnerEmail: "owner@example.com",
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "database provision lock is not configured")
	assert.Nil(t, resp)
}

func TestVeridianService_AcquireProvisionLock_PropagatesDatabaseError(t *testing.T) {
	svc, m := newVeridianService(t)
	lockErr := errors.New("postgres lock timeout")
	svc.planRepo = &testProvisionLockingPlanRepo{
		VeridianPlanRepository: m.planRepo,
		locks:                  make(map[string]chan struct{}),
		acquireErr:             lockErr,
	}

	resp, err := svc.Provision(t.Context(), domain.ProvisionInput{
		TenantID:   "ws-lock-timeout",
		OwnerEmail: "owner@example.com",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, lockErr)
	assert.ErrorContains(t, err, "acquire database provision lock")
	assert.Nil(t, resp)
}

func TestVeridianService_Provision_ConcurrentSameTenant(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()
	const (
		tenantID  = "ws-concurrent"
		ownerID   = "tenant-owner-id"
		ownerMail = "owner@example.com"
	)

	rootUser := &domain.User{ID: "root-id", Email: "root@veridian.site", Type: domain.UserTypeUser}
	ownerUser := &domain.User{ID: ownerID, Email: ownerMail, Type: domain.UserTypeUser}
	plan := &domain.VeridianPlan{WorkspaceID: tenantID, Plan: "free", Status: domain.PlanStatusActive}

	var planCreated atomic.Bool
	var workspaceCreated atomic.Bool
	var planLookupsInFlight atomic.Int32
	var maxPlanLookupsInFlight atomic.Int32

	// Le delai rend la regression deterministe : sans acquisition du verrou
	// avant ce premier lookup, les 5 goroutines entrent simultanement ici.
	m.planRepo.EXPECT().Get(ctx, tenantID).DoAndReturn(func(context.Context, string) (*domain.VeridianPlan, error) {
		current := planLookupsInFlight.Add(1)
		for {
			previous := maxPlanLookupsInFlight.Load()
			if current <= previous || maxPlanLookupsInFlight.CompareAndSwap(previous, current) {
				break
			}
		}
		defer planLookupsInFlight.Add(-1)
		time.Sleep(20 * time.Millisecond)
		if planCreated.Load() {
			return plan, nil
		}
		return nil, sql.ErrNoRows
	}).Times(5)

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), "root@veridian.site").Return(rootUser, nil).Times(6)
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).Times(7)
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).Times(7)

	m.workspace.EXPECT().GetWorkspace(gomock.Any(), tenantID).DoAndReturn(func(context.Context, string) (*domain.Workspace, error) {
		if workspaceCreated.Load() {
			return &domain.Workspace{ID: tenantID}, nil
		}
		return nil, errors.New("not found")
	}).Times(5)

	m.user.EXPECT().GetUserByEmail(ctx, ownerMail).
		Return(nil, &domain.ErrUserNotFound{Message: "not found"}).Times(1)
	m.userRepo.EXPECT().CreateUser(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, user *domain.User) error {
		user.ID = ownerID
		return nil
	}).Times(1)
	m.workspace.EXPECT().CreateWorkspace(
		gomock.Any(), tenantID, tenantID,
		gomock.Any(), gomock.Any(), gomock.Any(),
		"UTC", gomock.Any(), "en", gomock.Any(),
	).DoAndReturn(func(context.Context, string, string, string, string, string, string, domain.FileManagerSettings, string, []string) (*domain.Workspace, error) {
		workspaceCreated.Store(true)
		return &domain.Workspace{ID: tenantID}, nil
	}).Times(1)
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), tenantID, ownerID, "member", gomock.Any()).Return(nil).Times(1)
	m.workspace.EXPECT().CreateAPIKey(gomock.Any(), tenantID, "veridian-api-"+tenantID).
		Return("sk_test_only_once", "veridian-api-ws-concurrent@ws-concurrent.notifuse", nil).Times(1)
	m.userRepo.EXPECT().MarkVeridianManaged(ctx, "veridian-api-ws-concurrent@ws-concurrent.notifuse").Return(nil).Times(1)
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), tenantID, ownerID, rootUser.ID).Return(nil).Times(1)
	m.workspace.EXPECT().RemoveUserFromWorkspace(gomock.Any(), tenantID, rootUser.ID).Return(nil).Times(1)
	m.planRepo.EXPECT().Upsert(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, got *domain.VeridianPlan) error {
		plan.Plan = got.Plan
		planCreated.Store(true)
		return nil
	}).Times(1)

	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, tenantID).Return([]*domain.UserWorkspaceWithEmail{{
		UserWorkspace: domain.UserWorkspace{UserID: ownerID, WorkspaceID: tenantID, Role: "owner"},
		Email:         ownerMail,
		Type:          domain.UserTypeUser,
	}}, nil).Times(4)
	m.userRepo.EXPECT().GetUserByID(ctx, ownerID).Return(ownerUser, nil).Times(4)
	m.user.EXPECT().GenerateMagicCodeForVeridian(ctx, ownerMail, tenantID).
		Return("magic-code", time.Now().Add(15*time.Minute), nil).Times(5)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantProvisioned, tenantID, gomock.Any()).Times(1)

	start := make(chan struct{})
	responses := make([]*domain.ProvisionResponse, 5)
	errs := make([]error, 5)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			responses[index], errs[index] = svc.Provision(ctx, domain.ProvisionInput{
				TenantID:   tenantID,
				OwnerEmail: ownerMail,
				Plan:       "free",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	createdCount := 0
	apiKeyCount := 0
	for i := range responses {
		require.NoError(t, errs[i], "provision concurrente %d", i)
		require.NotNil(t, responses[i], "provision concurrente %d", i)
		assert.Equal(t, tenantID, responses[i].WorkspaceID)
		if responses[i].Created {
			createdCount++
		}
		if responses[i].APIKey != "" {
			apiKeyCount++
		}
	}
	assert.Equal(t, int32(1), maxPlanLookupsInFlight.Load(), "le verrou doit preceder le premier lookup")
	assert.Equal(t, 1, createdCount, "une seule goroutine cree le tenant")
	assert.Equal(t, 1, apiKeyCount, "une seule API key est emise")
}
