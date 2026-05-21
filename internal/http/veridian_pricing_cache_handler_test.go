package http

// === Veridian patch — lot O (2026-05-21) ===
// Tests pour le handler debug GET /api/veridian/admin/pricing-cache.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePricingProvider implemente PricingCacheProvider pour les tests, sans
// avoir a demarrer le service de cron (qui spawn une goroutine).
type fakePricingProvider struct {
	snap service.PricingCacheSnapshot
}

func (f *fakePricingProvider) Snapshot() service.PricingCacheSnapshot {
	return f.snap
}

// newHandlerWithPricing construit un VeridianHandler isole pour tester
// uniquement handlePricingCache, sans toucher au cache paywall ni au
// service principal.
func newHandlerWithPricing(p PricingCacheProvider) *VeridianHandler {
	h := &VeridianHandler{
		logger: logger.NewLogger(),
	}
	if p != nil {
		h.SetPricingSync(p)
	}
	return h
}

// TestVeridianHandlePricingCache_ReturnsSnapshot verifie le happy path :
// snapshot complet renvoye au caller en JSON.
func TestVeridianHandlePricingCache_ReturnsSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	prov := &fakePricingProvider{snap: service.PricingCacheSnapshot{
		Catalog: &service.PricingCatalog{
			Plans: map[string]service.PlanCatalogEntry{
				"notifuse-pro": {
					ID: "notifuse-pro", Tier: "pro", PriceEUR: 29,
				},
			},
			Version:     "0.1.0",
			GeneratedAt: now,
		},
		LastFetchedAt:  &now,
		LastSuccessAt:  &now,
		SourceURL:      "https://hub.veridian.site/api/pricing/plans",
		Stale:          false,
		StaleThreshold: "2h0m0s",
	}}
	h := newHandlerWithPricing(prov)

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/pricing-cache", nil)
	rec := httptest.NewRecorder()
	h.handlePricingCache(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.PricingCacheSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "https://hub.veridian.site/api/pricing/plans", resp.SourceURL)
	require.NotNil(t, resp.Catalog)
	assert.Equal(t, "0.1.0", resp.Catalog.Version)
	require.Contains(t, resp.Catalog.Plans, "notifuse-pro")
}

// TestVeridianHandlePricingCache_503WhenProviderMissing verifie qu'en
// l'absence de pricing sync injecte, le handler retourne 503 (pas 200
// silencieusement vide).
func TestVeridianHandlePricingCache_503WhenProviderMissing(t *testing.T) {
	h := newHandlerWithPricing(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/pricing-cache", nil)
	rec := httptest.NewRecorder()
	h.handlePricingCache(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestVeridianHandlePricingCache_ExposesLastErrorWhenStale verifie que
// le snapshot stale + last_error remonte bien au caller (utile pour
// alerter Robert via curl).
func TestVeridianHandlePricingCache_ExposesLastErrorWhenStale(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	prov := &fakePricingProvider{snap: service.PricingCacheSnapshot{
		Catalog:        nil, // pas encore peuple
		LastFetchedAt:  &now,
		LastError:      "unexpected status 502 from https://hub.veridian.site/...",
		SourceURL:      "https://hub.veridian.site/api/pricing/plans",
		Stale:          true,
		StaleThreshold: "2h0m0s",
	}}
	h := newHandlerWithPricing(prov)

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/pricing-cache", nil)
	rec := httptest.NewRecorder()
	h.handlePricingCache(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.PricingCacheSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Stale)
	assert.Contains(t, resp.LastError, "502")
	assert.Nil(t, resp.Catalog)
}

// TestVeridianSetPricingSync_Idempotent verifie qu'on peut overrider
// le provider (utile pour les tests d'integration qui veulent swap le
// service apres construction du handler).
func TestVeridianSetPricingSync_Idempotent(t *testing.T) {
	h := newHandlerWithPricing(nil)
	assert.Nil(t, h.pricingSync)

	p1 := &fakePricingProvider{}
	h.SetPricingSync(p1)
	assert.NotNil(t, h.pricingSync)

	p2 := &fakePricingProvider{}
	h.SetPricingSync(p2)
	assert.Same(t, p2, h.pricingSync.(*fakePricingProvider))
}

// TestVeridianHandlePricingCache_RegisteredInRoutes verifie que la route
// GET /api/veridian/admin/pricing-cache est bien enregistree dans le mux
// (regression test : si quelqu'un supprime mux.Handle, ce test casse).
//
// Auth HMAC : on s'attend a 401/403 quand on appelle sans signature
// (pas a 404). C'est suffisant pour valider que la route est cablee.
func TestVeridianHandlePricingCache_RegisteredInRoutes(t *testing.T) {
	mux := http.NewServeMux()
	h := newHandlerWithPricing(&fakePricingProvider{snap: service.PricingCacheSnapshot{
		SourceURL: "https://hub.veridian.site/api/pricing/plans",
	}})
	h.RegisterRoutes(mux, "test-secret-for-route-check")

	// Appel sans signature HMAC → middleware doit refuser, donc != 404.
	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/pricing-cache", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Le code exact depend du middleware HMAC (401 ou 403), mais surement pas 404
	// (route absente) ni 200 (auth bypass).
	assert.NotEqual(t, http.StatusNotFound, rec.Code, "route should be registered in mux")
	assert.NotEqual(t, http.StatusOK, rec.Code, "HMAC middleware should refuse unsigned request")
}
