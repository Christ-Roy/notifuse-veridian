package http

// === Veridian patch ===
// Tests focalises sur le handler /api/veridian/admin/cache/invalidate.
// Le reste du VeridianHandler est couvert via e2e Playwright (cf
// tests/e2e-veridian/specs/) et via les tests du service en-dessous
// (internal/service/veridian_service_test.go). Les tests unitaires
// complets sur tous les endpoints sont planifies dans Task #11.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper : invoque handleInvalidateCache sans middleware HMAC pour isoler
// la logique du handler. Le test du middleware HMAC se fait separement
// dans veridian_hmac_test.go.
func newHandlerWithCache(cache *middleware.PaywallCache) *VeridianHandler {
	h := &VeridianHandler{
		logger: logger.NewLogger(),
	}
	if cache != nil {
		h.SetPaywallCache(cache)
	}
	return h
}

func TestVeridianHandleInvalidateCache_OK(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)

	body := bytes.NewReader([]byte(`{"workspace_id":"ws-1"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cache/invalidate", body)
	rec := httptest.NewRecorder()

	h.handleInvalidateCache(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["workspace_id"])
	assert.Equal(t, true, resp["invalidated"])
}

func TestVeridianHandleInvalidateCache_IdempotentOnUnknownWorkspace(t *testing.T) {
	// Workspace pas dans le cache : Invalidate doit etre idempotent (no-op
	// silencieux, retourne 200). Pas d'erreur "not found" car le caller
	// (Hub) n'a pas a savoir si on avait deja une entree en cache.
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)

	body := bytes.NewReader([]byte(`{"workspace_id":"ws-doesnotexist"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cache/invalidate", body)
	rec := httptest.NewRecorder()

	h.handleInvalidateCache(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleInvalidateCache_MissingWorkspaceID(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)

	body := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cache/invalidate", body)
	rec := httptest.NewRecorder()

	h.handleInvalidateCache(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace_id is required")
}

func TestVeridianHandleInvalidateCache_EmptyWorkspaceID(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)

	body := bytes.NewReader([]byte(`{"workspace_id":""}`))
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cache/invalidate", body)
	rec := httptest.NewRecorder()

	h.handleInvalidateCache(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleInvalidateCache_InvalidJSON(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)

	body := bytes.NewReader([]byte(`{not valid json`))
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cache/invalidate", body)
	rec := httptest.NewRecorder()

	h.handleInvalidateCache(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestVeridianHandleInvalidateCache_NoCacheReturns503(t *testing.T) {
	// Mode self-hosted (pas de paywall, pas de cache injecte) :
	// l'endpoint retourne 503 explicitement plutot qu'un 200 silencieux faux.
	h := newHandlerWithCache(nil)

	body := bytes.NewReader([]byte(`{"workspace_id":"ws-1"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/cache/invalidate", body)
	rec := httptest.NewRecorder()

	h.handleInvalidateCache(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "paywall cache not initialized")
}
