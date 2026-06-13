package queue

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestProviderClassRateLimiter_AllowRespectsRate(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	// 1/min, burst 1 : le premier passe, le second est refusé immédiatement
	assert.True(t, prl.Allow("int-1", "google", 1))
	assert.False(t, prl.Allow("int-1", "google", 1))
}

func TestProviderClassRateLimiter_ClassesAreIndependent(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	// Épuiser le bucket google
	require.True(t, prl.Allow("int-1", "google", 1))
	require.False(t, prl.Allow("int-1", "google", 1))

	// corporate sur la même intégration n'est PAS impacté
	assert.True(t, prl.Allow("int-1", "corporate", 1))

	// google sur une AUTRE intégration n'est pas impacté non plus
	// (la réputation est par identité émettrice)
	assert.True(t, prl.Allow("int-2", "google", 1))
}

func TestProviderClassRateLimiter_FractionalRates(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	// 0.5/min (1 mail / 2 min) : accepté sans floor au 1/min
	limiter := prl.GetOrCreateLimiter("int-1", "google", 0.5)
	assert.Equal(t, rate.Limit(0.5/60.0), limiter.Limit())
}

func TestProviderClassRateLimiter_RateUpdate(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	first := prl.GetOrCreateLimiter("int-1", "google", 60)
	second := prl.GetOrCreateLimiter("int-1", "google", 120)

	// Même limiter (pas de recréation), débit mis à jour en place
	assert.Same(t, first, second)
	assert.Equal(t, rate.Limit(2.0), second.Limit())
}

func TestProviderClassRateLimiter_InvalidRateFallsBackToFloor(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	// Débit invalide : plancher 1/min, jamais de blocage définitif ni panic
	limiter := prl.GetOrCreateLimiter("int-1", "google", 0)
	assert.Equal(t, rate.Limit(1.0/60.0), limiter.Limit())

	limiter = prl.GetOrCreateLimiter("int-1", "microsoft", -5)
	assert.Equal(t, rate.Limit(1.0/60.0), limiter.Limit())
}

// Le providerClassLimiter est UNIQUE et PARTAGÉ entre toutes les goroutines
// workspace du worker pool (worker.go:processAllWorkspaces lance jusqu'à
// WorkerCount workspaces en parallèle). On doit prouver l'absence de data race
// sous accès concurrent : Allow/GetOrCreateLimiter (création + SetLimit) +
// GetStats (Range) + Clear, sur des clés partagées ET distinctes. À lancer avec
// -race (CI : go test -race).
func TestProviderClassRateLimiter_ConcurrentAccessNoRace(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	const goroutines = 24
	const iterations = 400
	classes := []string{"google", "microsoft", "yahoo_aol", "freemail_fr", "corporate"}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			// Moitié des goroutines tapent une intégration PARTAGÉE (contention
			// max sur les mêmes buckets), l'autre moitié une intégration propre.
			integrationID := "shared-int"
			if g%2 == 0 {
				integrationID = fmt.Sprintf("int-%d", g)
			}
			for i := 0; i < iterations; i++ {
				class := classes[i%len(classes)]
				// Débit qui varie → force des SetLimit concurrents sur un bucket
				// éventuellement partagé (le chemin TOCTOU du Load-hit).
				rate := float64(1 + i%120)
				prl.Allow(integrationID, class, rate)
				if i%50 == 0 {
					_ = prl.GetStats()
				}
				if i%97 == 0 {
					prl.Clear()
				}
			}
		}(g)
	}
	wg.Wait()

	// Pas d'assertion fonctionnelle (les débits sont chaotiques par design) :
	// le seul critère est l'absence de race/panic, garanti par -race + le fait
	// d'arriver ici sans crash.
	_ = prl.GetStats()
}

func TestProviderClassRateLimiter_GetStatsAndClear(t *testing.T) {
	prl := NewProviderClassRateLimiter()

	prl.Allow("int-1", "google", 60)
	prl.Allow("int-1", "corporate", 600)

	stats := prl.GetStats()
	require.Len(t, stats, 2)
	require.Contains(t, stats, "int-1|google")
	require.Contains(t, stats, "int-1|corporate")
	assert.Equal(t, 60.0, stats["int-1|google"].RatePerMinute)
	assert.Equal(t, 600.0, stats["int-1|corporate"].RatePerMinute)

	prl.Clear()
	assert.Empty(t, prl.GetStats())
}
