package queue

import (
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
