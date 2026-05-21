package service

// === Veridian patch — Lot K (2026-05-21) ===
// Tests du cleanup scheduler grace.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVeridianAPIKeyGraceCleanupService_DefaultInterval(t *testing.T) {
	s := NewVeridianAPIKeyGraceCleanupService(nil, logger.NewLogger(), 0)
	require.NotNil(t, s)
	assert.Equal(t, 1*time.Minute, s.interval)
}

func TestNewVeridianAPIKeyGraceCleanupService_CustomInterval(t *testing.T) {
	s := NewVeridianAPIKeyGraceCleanupService(nil, logger.NewLogger(), 30*time.Second)
	assert.Equal(t, 30*time.Second, s.interval)
}

func TestNewVeridianAPIKeyGraceCleanupService_NegativeIntervalUsesDefault(t *testing.T) {
	s := NewVeridianAPIKeyGraceCleanupService(nil, logger.NewLogger(), -1*time.Second)
	assert.Equal(t, 1*time.Minute, s.interval)
}

func TestVeridianAPIKeyGraceCleanupService_Start_NilSvc_NoPanic(t *testing.T) {
	// svc nil → Start retourne sans demarrer la goroutine. Pas de panic.
	s := NewVeridianAPIKeyGraceCleanupService(nil, logger.NewLogger(), 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx) // doit retourner immediatement
	// On verifie qu'on peut continuer (pas de blocking goroutine).
	time.Sleep(20 * time.Millisecond)
}

// Test integration : svc reel (veridianService avec grace repo) → la goroutine
// doit appeler RunAPIKeyGraceCleanupOnce a chaque tick, qui delegue a
// graceRepo.ListExpired.
func TestVeridianAPIKeyGraceCleanupService_TickerInvocations(t *testing.T) {
	svc, _, graceRepo := newVeridianServiceWithGrace(t)
	calls := atomic.Int32{}
	graceRepo.EXPECT().ListExpired(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ time.Time) ([]*domain.APIKeyGraceEntry, error) {
			calls.Add(1)
			return nil, nil
		}).AnyTimes()

	cleanup := NewVeridianAPIKeyGraceCleanupService(svc, logger.NewLogger(), 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cleanup.Start(ctx)

	// Attendre 2-3 ticks.
	time.Sleep(80 * time.Millisecond)
	cancel()
	// Laisser le temps a la goroutine de se terminer.
	time.Sleep(20 * time.Millisecond)

	assert.True(t, calls.Load() >= 2, "expected ≥2 ticks, got %d", calls.Load())
}

// Test integration : svc qui n'implemente PAS RunAPIKeyGraceCleanupOnce
// (MockVeridianService genere par mockgen). runOnce log warn et continue.
func TestVeridianAPIKeyGraceCleanupService_SvcWithoutCleanupMethod(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// MockVeridianService satisfait domain.VeridianService MAIS n'a pas
	// la methode RunAPIKeyGraceCleanupOnce (cette derniere est sur le type
	// concret *veridianService uniquement).
	cleanup := NewVeridianAPIKeyGraceCleanupService(
		stubVeridianServiceWithoutCleanup{},
		logger.NewLogger(),
		20*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cleanup.Start(ctx)

	// Attendre 1-2 ticks — runOnce doit log warn mais pas crasher.
	time.Sleep(40 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// stubVeridianServiceWithoutCleanup est un stub minimal qui implemente
// domain.VeridianService mais SANS la methode RunAPIKeyGraceCleanupOnce.
// On l'utilise pour tester le path "type assertion echoue" dans
// VeridianAPIKeyGraceCleanupService.runOnce.
//
// Toutes les methodes retournent zero values — elles ne sont jamais appelees
// dans le test.
type stubVeridianServiceWithoutCleanup struct{}

func (stubVeridianServiceWithoutCleanup) Provision(context.Context, domain.ProvisionInput) (*domain.ProvisionResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) UpdatePlan(context.Context, domain.UpdatePlanInput) (*domain.UpdatePlanResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) Suspend(context.Context, domain.SuspendInput) error {
	return nil
}
func (stubVeridianServiceWithoutCleanup) Resume(context.Context, domain.ResumeInput) error { return nil }
func (stubVeridianServiceWithoutCleanup) SoftDelete(context.Context, domain.SoftDeleteInput) (*domain.SoftDeleteResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) Restore(context.Context, domain.RestoreInput) (*domain.RestoreResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) Purge(context.Context, domain.PurgeInput) (*domain.PurgeResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) Touch(context.Context, string) (*domain.TouchResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) UsageSummary(context.Context, string) (*domain.UsageSummaryResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) GetStatus(context.Context, string) (*domain.StatusResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) GenerateMagicLink(context.Context, string, string) (*domain.MagicLinkResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) WipeTestTenants(context.Context, domain.WipeTestTenantsInput) (*domain.WipeTestTenantsResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) ListTenants(context.Context, domain.ListTenantsInput) (*domain.ListTenantsResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) AttachOwner(context.Context, domain.AttachOwnerInput) (*domain.AttachOwnerResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) Health(context.Context, string) (*domain.TenantHealthResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) GrantUnlimited(context.Context, domain.GrantUnlimitedInput) (*domain.GrantUnlimitedResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) GetLimits(context.Context, string) (*domain.LimitsResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) AttachMember(context.Context, domain.AttachMemberInput) (*domain.AttachMemberResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) LookupByEmail(context.Context, string) (*domain.DiscoveryResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) RotateAPIKey(context.Context, domain.RotateAPIKeyInput) (*domain.RotateAPIKeyResponse, error) {
	return nil, nil
}
func (stubVeridianServiceWithoutCleanup) TransferOwner(context.Context, domain.TransferOwnerInput) (*domain.TransferOwnerResponse, error) {
	return nil, nil
}
