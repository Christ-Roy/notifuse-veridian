package service

import (
	"context"
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

// makeWorkspaces génère une liste de workspaces avec ids + âges contrôlés
// pour les tests. Chaque entrée : (id, ageBeforeNow). ageBeforeNow = 0 →
// CreatedAt = now (frais), 2h → CreatedAt = now-2h (orphan candidat).
func makeWorkspaces(items ...struct {
	ID  string
	Age time.Duration
}) []*domain.Workspace {
	now := time.Now()
	out := make([]*domain.Workspace, 0, len(items))
	for _, it := range items {
		out = append(out, &domain.Workspace{
			ID:        it.ID,
			CreatedAt: now.Add(-it.Age),
		})
	}
	return out
}

// === GARDE-FOU PROD : ne JAMAIS démarrer en non-staging ===

func TestVeridianTestTenantsCleanup_DisabledInProduction(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	// AUCUN appel à List ou WipeTestTenants ne doit arriver. gomock fail si
	// jamais un EXPECT manquant est appelé.

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "production", 10*time.Millisecond,
	)
	assert.False(t, svc.IsEnabled(), "IsEnabled doit être false en production")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	// Laisse le temps à un éventuel tick foireux de partir (il ne doit pas).
	time.Sleep(50 * time.Millisecond)
}

func TestVeridianTestTenantsCleanup_DisabledInDevelopment(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "development", 10*time.Millisecond,
	)
	assert.False(t, svc.IsEnabled())
	svc.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
}

func TestVeridianTestTenantsCleanup_DisabledOnEmptyEnv(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "", 10*time.Millisecond,
	)
	assert.False(t, svc.IsEnabled())
	svc.Start(context.Background())
	time.Sleep(30 * time.Millisecond)
}

func TestVeridianTestTenantsCleanup_EnabledOnStaging(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 10*time.Millisecond,
	)
	assert.True(t, svc.IsEnabled())
}

// === FONCTIONNEL : filtrage prefix + age + cap ===

// Wipe doit appeler WipeTestTenants UNIQUEMENT pour les workspaces qui
// matchent prefix `tst` ET ont CreatedAt < now-1h. Les frais et les autres
// prefixes sont ignorés.
func TestVeridianTestTenantsCleanup_FiltersOldTstOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().List(gomock.Any()).Return(makeWorkspaces(
		struct {
			ID  string
			Age time.Duration
		}{"tstold1", 2 * time.Hour},    // candidat
		struct {
			ID  string
			Age time.Duration
		}{"tstfresh1", 10 * time.Minute}, // trop frais → skip
		struct {
			ID  string
			Age time.Duration
		}{"tstold2", 3 * time.Hour},    // candidat
		struct {
			ID  string
			Age time.Duration
		}{"otherold", 5 * time.Hour},    // mauvais prefix → skip
		struct {
			ID  string
			Age time.Duration
		}{"canaryfree", 24 * time.Hour}, // safety, mais filtré déjà par prefix
	), nil).AnyTimes()

	done := make(chan []string, 1)
	veridianSvc.EXPECT().WipeTestTenants(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, in domain.WipeTestTenantsInput) (*domain.WipeTestTenantsResponse, error) {
			// Capture la 1ère liste reçue (run immédiat).
			select {
			case done <- in.TenantIDs:
			default:
			}
			return &domain.WipeTestTenantsResponse{Wiped: in.TenantIDs}, nil
		}).MinTimes(1)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 10*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	select {
	case ids := <-done:
		assert.ElementsMatch(t, []string{"tstold1", "tstold2"}, ids,
			"seuls les tst* > 1h doivent être candidats")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WipeTestTenants jamais appelé")
	}
}

// Si aucun candidat (tous frais), WipeTestTenants ne doit PAS être appelé,
// mais le tick log info "nothing to wipe" et la télémétrie est mise à jour.
func TestVeridianTestTenantsCleanup_NoOpWhenNoCandidates(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	listed := make(chan struct{}, 1)
	wsRepo.EXPECT().List(gomock.Any()).DoAndReturn(func(_ context.Context) ([]*domain.Workspace, error) {
		select {
		case listed <- struct{}{}:
		default:
		}
		return makeWorkspaces(
			struct {
				ID  string
				Age time.Duration
			}{"tstfresh1", 5 * time.Minute},
			struct {
				ID  string
				Age time.Duration
			}{"tstfresh2", 30 * time.Minute},
		), nil
	}).MinTimes(1)
	// Pas de EXPECT sur WipeTestTenants → gomock fail si appelé.

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 10*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	select {
	case <-listed:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("List jamais appelé")
	}
	// Laisse passer un peu pour confirmer absence de Wipe.
	time.Sleep(30 * time.Millisecond)
}

// Cap : si > 100 candidats orphelins, seuls 100 sont envoyés au wipe (rate-
// limit anti-runaway).
func TestVeridianTestTenantsCleanup_CapsAt100PerTick(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	// Génère 250 orphelins tst* tous > 2h.
	items := make([]struct {
		ID  string
		Age time.Duration
	}, 0, 250)
	for i := 0; i < 250; i++ {
		items = append(items, struct {
			ID  string
			Age time.Duration
		}{
			ID:  "tstorphan" + string(rune('0'+i%10)) + string(rune('a'+(i/10)%26)) + string(rune('a'+(i/260)%26)),
			Age: 2 * time.Hour,
		})
	}
	// Note : les IDs peuvent doublonner mais ça ne change pas le test de cap.
	wsRepo.EXPECT().List(gomock.Any()).Return(makeWorkspaces(items...), nil).AnyTimes()

	captured := make(chan int, 1)
	veridianSvc.EXPECT().WipeTestTenants(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, in domain.WipeTestTenantsInput) (*domain.WipeTestTenantsResponse, error) {
			select {
			case captured <- len(in.TenantIDs):
			default:
			}
			return &domain.WipeTestTenantsResponse{}, nil
		}).MinTimes(1)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 10*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	select {
	case got := <-captured:
		assert.Equal(t, testTenantsCleanupMaxPerTick, got,
			"WipeTestTenants doit recevoir au plus %d ids", testTenantsCleanupMaxPerTick)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WipeTestTenants jamais appelé")
	}
}

// Erreur List → log + scheduler continue (best-effort).
func TestVeridianTestTenantsCleanup_LogsListErrorAndContinues(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	calls := make(chan struct{}, 5)
	wsRepo.EXPECT().List(gomock.Any()).DoAndReturn(func(_ context.Context) ([]*domain.Workspace, error) {
		calls <- struct{}{}
		return nil, errors.New("db down")
	}).MinTimes(2)
	// Pas d'EXPECT Wipe → ne doit pas être appelé.

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 10*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	for i := 0; i < 2; i++ {
		select {
		case <-calls:
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("call #%d jamais arrivé — scheduler stoppé après List error ?", i+1)
		}
	}
}

// Erreur WipeTestTenants → log + scheduler continue.
func TestVeridianTestTenantsCleanup_LogsWipeErrorAndContinues(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().List(gomock.Any()).Return(makeWorkspaces(
		struct {
			ID  string
			Age time.Duration
		}{"tstorphan1", 2 * time.Hour},
	), nil).MinTimes(2)

	wipes := make(chan struct{}, 5)
	veridianSvc.EXPECT().WipeTestTenants(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.WipeTestTenantsInput) (*domain.WipeTestTenantsResponse, error) {
			wipes <- struct{}{}
			return nil, errors.New("wipe failed")
		}).MinTimes(2)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 10*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	for i := 0; i < 2; i++ {
		select {
		case <-wipes:
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("wipe call #%d jamais arrivé après erreur", i+1)
		}
	}
}

// === Télémétrie : Stats() ===

func TestVeridianTestTenantsCleanup_Stats_CountsLive(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().List(gomock.Any()).Return(makeWorkspaces(
		struct {
			ID  string
			Age time.Duration
		}{"tstold1", 2 * time.Hour},
		struct {
			ID  string
			Age time.Duration
		}{"tstold2", 3 * time.Hour},
		struct {
			ID  string
			Age time.Duration
		}{"tstfresh", 5 * time.Minute},
		struct {
			ID  string
			Age time.Duration
		}{"otherold", 24 * time.Hour}, // ignoré (pas prefix tst)
	), nil)

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 0,
	)
	stats, err := svc.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, stats.TotalTestTenants)
	assert.Equal(t, 2, stats.OrphansOlderThan1h)
	assert.True(t, stats.Enabled)
	assert.Zero(t, stats.LastCleanupWipedCount, "pas encore tourné")
	assert.True(t, stats.LastAutoCleanupAt.IsZero())
}

func TestVeridianTestTenantsCleanup_Stats_DisabledOnProd(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "production", 0,
	)
	stats, err := svc.Stats(context.Background())
	require.NoError(t, err)
	assert.False(t, stats.Enabled, "Enabled doit être false en prod")
}

func TestVeridianTestTenantsCleanup_Stats_ListErrorReturnsPartial(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db down"))

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 0,
	)
	stats, err := svc.Stats(context.Background())
	require.Error(t, err, "doit propager l'erreur List")
	require.NotNil(t, stats, "mais quand même retourner stats partielles")
	assert.True(t, stats.Enabled)
	assert.Zero(t, stats.TotalTestTenants)
}

// === Lifecycle goroutine ===

func TestVeridianTestTenantsCleanup_StopsOnContextCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	wsRepo.EXPECT().List(gomock.Any()).Return(nil, nil).AnyTimes()

	svc := NewVeridianTestTenantsCleanupService(
		veridianSvc, wsRepo, logger.NewLogger(), "staging", 50*time.Millisecond,
	)
	ctx, cancel := context.WithCancel(context.Background())

	svc.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
}

// Interval par défaut = 30 min si 0/négatif.
func TestVeridianTestTenantsCleanup_DefaultsInterval(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	veridianSvc := mocks.NewMockVeridianService(ctrl)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)

	for _, in := range []time.Duration{0, -1 * time.Minute} {
		svc := NewVeridianTestTenantsCleanupService(
			veridianSvc, wsRepo, logger.NewLogger(), "staging", in,
		)
		assert.Equal(t, testTenantsCleanupInterval, svc.interval,
			"interval %v devrait fallback à %v", in, testTenantsCleanupInterval)
	}
}
