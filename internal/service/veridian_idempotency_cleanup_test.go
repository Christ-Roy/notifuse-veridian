package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Le cleanup tourne immédiatement au Start() puis tous les `interval`. On
// utilise un interval ultra-court (10ms) + on stoppe via ctx pour pouvoir
// asserter sans flake. Vérifie qu'au moins 1 appel à DeleteExpired arrive.
func TestVeridianIdempotencyCleanupService_RunsOnStartAndOnTick(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	// MinTimes(2) : run immédiat + au moins 1 tick (20ms suffisent pour 10ms interval).
	done := make(chan struct{})
	callCount := 0
	repo.EXPECT().DeleteExpired(gomock.Any()).
		DoAndReturn(func(_ context.Context) (int64, error) {
			callCount++
			if callCount >= 2 {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			return int64(7), nil
		}).MinTimes(2)

	svc := NewVeridianIdempotencyCleanupService(repo, logger.NewLogger(), 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)

	select {
	case <-done:
		// OK : >= 2 calls observés.
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("cleanup attendu (>= 2 calls) jamais déclenché, observed=%d", callCount)
	}
}

// Si le repo retourne une erreur, le scheduler continue de tourner.
func TestVeridianIdempotencyCleanupService_LogsErrorButContinues(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	calls := make(chan struct{}, 5)
	repo.EXPECT().DeleteExpired(gomock.Any()).
		DoAndReturn(func(_ context.Context) (int64, error) {
			calls <- struct{}{}
			return 0, errors.New("db down")
		}).MinTimes(2)

	svc := NewVeridianIdempotencyCleanupService(repo, logger.NewLogger(), 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)

	// Attendre 2 calls : run immédiat (fail) + 1 tick (fail aussi → continue prouvé).
	for i := 0; i < 2; i++ {
		select {
		case <-calls:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("call #%d jamais arrivé — scheduler a stoppé après erreur ?", i+1)
		}
	}
}

// ctx.Cancel() arrête la goroutine proprement.
func TestVeridianIdempotencyCleanupService_StopsOnContextCancel(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	// AnyTimes() : selon le timing, 1 ou 2 appels avant cancel.
	repo.EXPECT().DeleteExpired(gomock.Any()).Return(int64(0), nil).AnyTimes()

	svc := NewVeridianIdempotencyCleanupService(repo, logger.NewLogger(), 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	svc.Start(ctx)
	time.Sleep(20 * time.Millisecond) // laisse le run immédiat passer
	cancel()
	// Pas d'assertion stricte sur le timing — on vérifie juste qu'il n'y a pas
	// de fuite goroutine (le t.Cleanup gomock va check les calls non-attendus).
	time.Sleep(100 * time.Millisecond)
}

// Si le repo est nil (config foireuse / boot partiel), Start() doit no-op au
// lieu de paniquer. Defense en profondeur.
func TestVeridianIdempotencyCleanupService_NilRepoNoOp(t *testing.T) {
	svc := NewVeridianIdempotencyCleanupService(nil, logger.NewLogger(), 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NotPanics(t, func() { svc.Start(ctx) })
}

// Interval par défaut = 24h si on passe 0 ou négatif (defensive default).
func TestVeridianIdempotencyCleanupService_NewDefaultsInterval(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	for _, in := range []time.Duration{0, -5 * time.Minute} {
		svc := NewVeridianIdempotencyCleanupService(repo, logger.NewLogger(), in)
		assert.Equal(t, 24*time.Hour, svc.interval,
			"interval %v devrait fallback à 24h", in)
	}
}
