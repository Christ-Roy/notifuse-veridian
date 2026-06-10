package queue

import (
	"sync"

	"golang.org/x/time/rate"
)

// Veridian — second étage de rate-limiting, par classe de provider
// destinataire (cold outbound). Clone du pattern IntegrationRateLimiter
// (même golang.org/x/time/rate, même sync.Map), keyé par
// {integrationID, classe} : la réputation à protéger est celle de l'identité
// émettrice (domaine/IP de l'intégration) auprès de chaque classe receveuse —
// deux broadcasts sur la même intégration partagent donc le même bucket gmail.
//
// Contrairement au limiter émetteur (Wait bloquant), ce limiter est consommé
// en mode non-bloquant (Allow) par le worker : une classe saturée re-planifie
// l'entrée au lieu de bloquer le pipeline. Cf. veridian_provider_throttle.go.

// ProviderClassRateLimiter manages rate limits per {integration, provider class}
type ProviderClassRateLimiter struct {
	limiters sync.Map // map[integrationID+"|"+class]*rate.Limiter
}

// NewProviderClassRateLimiter creates a new ProviderClassRateLimiter
func NewProviderClassRateLimiter() *ProviderClassRateLimiter {
	return &ProviderClassRateLimiter{}
}

func providerClassKey(integrationID, class string) string {
	return integrationID + "|" + class
}

// GetOrCreateLimiter returns a rate limiter for the {integration, class} pair,
// creating one if needed. The rate limiter is updated if the rate has changed.
// ratePerMinute accepte les fractions (0.5 = 1 email / 2 min) pour couvrir les
// cadences warm-up très lentes sans quota journalier.
func (prl *ProviderClassRateLimiter) GetOrCreateLimiter(integrationID, class string, ratePerMinute float64) *rate.Limiter {
	ratePerSecond := ratePerMinute / 60.0
	if ratePerSecond <= 0 {
		// Garde-fou : un débit invalide ne doit jamais bloquer définitivement —
		// on retombe sur le plancher du limiter émetteur (1/min).
		ratePerSecond = 1.0 / 60.0
	}

	key := providerClassKey(integrationID, class)
	if existing, ok := prl.limiters.Load(key); ok {
		limiter := existing.(*rate.Limiter)
		if limiter.Limit() != rate.Limit(ratePerSecond) {
			limiter.SetLimit(rate.Limit(ratePerSecond))
		}
		return limiter
	}

	// Burst of 1: strict spacing, same as the integration limiter
	limiter := rate.NewLimiter(rate.Limit(ratePerSecond), 1)
	actual, _ := prl.limiters.LoadOrStore(key, limiter)
	return actual.(*rate.Limiter)
}

// Allow checks if sending to this provider class is allowed immediately
// without blocking. Returns true if allowed (consumes a token).
func (prl *ProviderClassRateLimiter) Allow(integrationID, class string, ratePerMinute float64) bool {
	limiter := prl.GetOrCreateLimiter(integrationID, class, ratePerMinute)
	return limiter.Allow()
}

// GetStats returns statistics about all provider-class rate limiters,
// keyed by "integrationID|class".
func (prl *ProviderClassRateLimiter) GetStats() map[string]RateLimiterStats {
	stats := make(map[string]RateLimiterStats)
	prl.limiters.Range(func(key, value interface{}) bool {
		limiter := value.(*rate.Limiter)
		stats[key.(string)] = RateLimiterStats{
			RatePerSecond:   float64(limiter.Limit()),
			RatePerMinute:   float64(limiter.Limit()) * 60,
			TokensAvailable: limiter.Tokens(),
			Burst:           limiter.Burst(),
		}
		return true
	})
	return stats
}

// Clear removes all rate limiters (useful for testing or shutdown)
func (prl *ProviderClassRateLimiter) Clear() {
	prl.limiters.Range(func(key, value interface{}) bool {
		prl.limiters.Delete(key)
		return true
	})
}
