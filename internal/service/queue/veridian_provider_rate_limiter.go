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

// AllowSeeded is Allow, but when a key is touched for the FIRST TIME since
// process start, consults recentlySent before granting the fresh burst
// token.
//
// Veridian fork (correctif 2026-10-05, incident robertbrunon : 181 envois
// entre 8h et 9h malgré un débit par classe réglé pour ~27/h par infra). Ce
// limiter est EN MÉMOIRE PURE (cf. tête de fichier) : chaque redémarrage du
// worker (déploiement, crash, OOM — le job notifuse s'est redéployé plusieurs
// fois par jour début octobre, cf. `nomad-v raw job history notifuse`) lui
// fait perdre tout son état et regrant un token "gratuit" par clé
// {intégration, classe} dès le premier appel. Juste au moment où la fenêtre
// d'envoi (8h) libère tout le backlog accumulé dans la nuit, un redémarrage
// proche de cet instant fait repartir TOUTES les clés à burst plein en même
// temps : 11 classes × 2 intégrations = 22 jetons gratuits d'un coup, répétés
// à chaque redémarrage de la fenêtre — largement assez pour expliquer un
// multiple du débit nominal combiné (~54/h) observé en une heure.
//
// recentlySent répond "un envoi DURABLE (message_history) existe déjà pour
// cette clé dans l'intervalle nominal (60/ratePerMinute secondes)". Si oui,
// le jeton initial est immédiatement consommé (le process vient de perdre la
// mémoire d'un envoi qui, lui, a réellement eu lieu il y a moins d'un
// intervalle) : pas de jeton gratuit en plus. Si non (clé vraiment neuve,
// ou rien envoyé récemment), le comportement est inchangé (burst normal).
// recentlySent n'est appelé QUE sur un premier contact avec cette clé — coût
// borné au nombre de clés distinctes, jamais par appel.
func (prl *ProviderClassRateLimiter) AllowSeeded(integrationID, class string, ratePerMinute float64, recentlySent func() bool) bool {
	key := providerClassKey(integrationID, class)
	_, alreadyTouched := prl.limiters.Load(key)
	limiter := prl.GetOrCreateLimiter(integrationID, class, ratePerMinute)
	if !alreadyTouched && recentlySent != nil && recentlySent() {
		// Consomme le jeton de démarrage sans l'accorder à CET appel : un
		// envoi durable a déjà eu lieu dans l'intervalle, celui-ci doit
		// attendre comme n'importe quel appel qui suit un envoi récent.
		limiter.Allow()
		return false
	}
	return limiter.Allow()
}

// PeekSeeded répond comme AllowSeeded SANS rien consommer ni créer : un limiter
// jamais touché n'est pas amorcé (le vrai appel le fera), un limiter existant n'est
// pas débité. Sert aux explications de la fiche 62 : lire le débit d'une entrée
// qu'une autre porte bloque déjà ne doit pas priver une entrée qui, elle, enverra.
func (prl *ProviderClassRateLimiter) PeekSeeded(integrationID, class string, ratePerMinute float64, recentlySent func() bool) bool {
	v, touched := prl.limiters.Load(providerClassKey(integrationID, class))
	if !touched {
		return recentlySent == nil || !recentlySent()
	}
	return v.(*rate.Limiter).Tokens() >= 1
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
