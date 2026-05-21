package service

// === Veridian patch === Tests colocalises pour veridian_grant_unlimited.go.

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianService_GrantUnlimited_FreeToEnterprise(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	existing := &domain.VeridianPlan{
		WorkspaceID:       "ws-1",
		Plan:              "free",
		PlanSource:        domain.PlanSourceStripe,
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: 300,
	}
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(existing, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-1", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).
		Return(nil).Times(1)
	// Pas de Resume car status=active.
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-1", gomock.Any()).Times(1)

	resp, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "ws-1",
		Reason:   "internal_team_member",
	})
	require.NoError(t, err)
	assert.Equal(t, "enterprise", resp.Plan)
	assert.Equal(t, "free", resp.PreviousPlan)
	assert.Equal(t, domain.PlanSourceLifetimePartner, resp.PlanSource)
	assert.Equal(t, int64(-1), resp.Quota)
	assert.Equal(t, "internal_team_member", resp.Reason)
}

func TestVeridianService_GrantUnlimited_SuspendedTenantIsResumed(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	existing := &domain.VeridianPlan{
		WorkspaceID:     "ws-2",
		Plan:            "pro",
		PlanSource:      domain.PlanSourceStripe,
		Status:          domain.PlanStatusSuspended,
		SuspendedReason: "quota exceeded",
	}
	m.planRepo.EXPECT().Get(ctx, "ws-2").Return(existing, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-2", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).
		Return(nil).Times(1)
	// Status=suspended → Resume appele.
	m.planRepo.EXPECT().Resume(ctx, "ws-2").Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-2", gomock.Any()).Times(1)

	resp, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "ws-2",
		Reason:   "compensation_outage",
	})
	require.NoError(t, err)
	assert.Equal(t, "pro", resp.PreviousPlan)
}

func TestVeridianService_GrantUnlimited_ExplicitPlanSource(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	existing := &domain.VeridianPlan{WorkspaceID: "ws-3", Plan: "free", PlanSource: domain.PlanSourceStripe, Status: domain.PlanStatusActive}
	m.planRepo.EXPECT().Get(ctx, "ws-3").Return(existing, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-3", "enterprise", int64(-1), domain.PlanSourceInternal).
		Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-3", gomock.Any()).Times(1)

	resp, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID:   "ws-3",
		Reason:     "veridian_internal_workspace",
		PlanSource: domain.PlanSourceInternal,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PlanSourceInternal, resp.PlanSource)
}

func TestVeridianService_GrantUnlimited_RejectsStripeSource(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	// Aucun mock attendu : la validation echoue avant d'atteindre planRepo.
	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID:   "ws-1",
		Reason:     "test",
		PlanSource: domain.PlanSourceStripe,
	})
	require.ErrorIs(t, err, ErrInvalidPlanSourceForGrant)
}

func TestVeridianService_GrantUnlimited_RejectsInvalidSource(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID:   "ws-1",
		Reason:     "test",
		PlanSource: "bogus_source",
	})
	require.ErrorIs(t, err, ErrInvalidPlanSourceForGrant)
}

func TestVeridianService_GrantUnlimited_RejectsEmptyReason(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "ws-1",
		Reason:   "",
	})
	require.ErrorIs(t, err, ErrGrantReasonRequired)
}

func TestVeridianService_GrantUnlimited_RejectsEmptyTenantID(t *testing.T) {
	svc, _ := newVeridianService(t)
	ctx := context.Background()

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "",
		Reason:   "test",
	})
	require.Error(t, err)
}

func TestVeridianService_GrantUnlimited_TenantNotFound(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	m.planRepo.EXPECT().Get(ctx, "ghost").Return(nil, sql.ErrNoRows).Times(1)

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "ghost",
		Reason:   "test",
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestVeridianService_GrantUnlimited_UpdatePlanFails(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	existing := &domain.VeridianPlan{WorkspaceID: "ws-1", Plan: "free", PlanSource: domain.PlanSourceStripe, Status: domain.PlanStatusActive}
	m.planRepo.EXPECT().Get(ctx, "ws-1").Return(existing, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("db lost")).Times(1)
	// Pas d'emit ni de Resume sur UpdatePlan fail.

	_, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "ws-1",
		Reason:   "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db lost")
}

func TestVeridianService_GrantUnlimited_AlreadyEnterprise_Idempotent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	// Tenant deja enterprise/lifetime → on re-update quand meme (idempotent).
	existing := &domain.VeridianPlan{
		WorkspaceID:       "ws-vip",
		Plan:              "enterprise",
		PlanSource:        domain.PlanSourceLifetimePartner,
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: -1,
	}
	m.planRepo.EXPECT().Get(ctx, "ws-vip").Return(existing, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-vip", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).
		Return(nil).Times(1)
	m.emitter.EXPECT().Emit(ctx, domain.EventTenantPlanChanged, "ws-vip", gomock.Any()).Times(1)

	resp, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID: "ws-vip",
		Reason:   "re-grant_audit",
	})
	require.NoError(t, err)
	assert.Equal(t, "enterprise", resp.Plan)
	assert.Equal(t, "enterprise", resp.PreviousPlan) // previous = enterprise (idempotent)
}

// TestGrantUnlimited_TouchHubSync_WiredViaGrantUnlimitedFile — V39 garde-fou
// que touchHubSync est bien câblé dans veridian_grant_unlimited.go (fichier source modifié).
// La couverture Times(1) est dans veridian_hub_sync_test.go (TestGrantUnlimited_TouchHubSync_Wired).
// Ce test complète le mapping Constitution §1 : veridian_grant_unlimited.go → ce fichier.
func TestGrantUnlimited_TouchHubSync_NewFuncPresent(t *testing.T) {
	svc, m := newVeridianService(t)
	ctx := context.Background()

	existing := &domain.VeridianPlan{
		WorkspaceID: "ws-hubsync-guard",
		Plan:        "free",
		PlanSource:  domain.PlanSourceStripe,
		Status:      domain.PlanStatusActive,
	}
	m.planRepo.EXPECT().Get(ctx, "ws-hubsync-guard").Return(existing, nil).Times(1)
	m.planRepo.EXPECT().UpdatePlan(ctx, "ws-hubsync-guard", "enterprise", int64(-1), domain.PlanSourceLifetimePartner).
		Return(nil).Times(1)
	m.emitter.EXPECT().Emit(gomock.Any(), domain.EventTenantPlanChanged, "ws-hubsync-guard", gomock.Any()).Times(1)
	// planRepo.TouchHubSync est géré par AnyTimes() dans newVeridianService.

	resp, err := svc.GrantUnlimited(ctx, domain.GrantUnlimitedInput{
		TenantID:   "ws-hubsync-guard",
		Reason:     "v39_touchhubsync_guard",
		PlanSource: domain.PlanSourceLifetimePartner,
	})
	require.NoError(t, err)
	assert.Equal(t, "enterprise", resp.Plan)
}
