package http

// Tests du handler GET /api/veridian/admin/test-tenants-stats.
// Couvre :
//   - 503 si SetTestTenantsCleanup jamais appele (mode prod / boot partiel)
//   - 200 + body JSON quand Stats() success
//   - 500 si Stats() retourne erreur sans stats partielles
//   - 500 + partial_stats si Stats() retourne erreur ET stats non-nil (best-effort)

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStatsProvider est un stub TestTenantsCleanupStatsProvider in-process
// pour tester le handler sans demarrer un vrai cron / mock gomock.
type fakeStatsProvider struct {
	stats *service.TestTenantsCleanupStats
	err   error
}

func (f *fakeStatsProvider) Stats(_ context.Context) (*service.TestTenantsCleanupStats, error) {
	return f.stats, f.err
}

func newHandlerForStats(p TestTenantsCleanupStatsProvider) *VeridianHandler {
	h := &VeridianHandler{logger: logger.NewLogger()}
	if p != nil {
		h.SetTestTenantsCleanup(p)
	}
	return h
}

// helper : GET handler direct (pas de middleware HMAC, isole le handler).
func getStats(t *testing.T, h *VeridianHandler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/test-tenants-stats", nil)
	rec := httptest.NewRecorder()
	h.handleTestTenantsStats(rec, req)
	return rec
}

// Le handler renvoie 503 si SetTestTenantsCleanup jamais appele. Critique :
// audit prod doit verifier que le cron n'est pas branche en prod (garde-fou
// DeployEnv). Si Robert curl en prod, il DOIT voir 503 (pas un 200 muet).
func TestHandleTestTenantsStats_NotInitialized503(t *testing.T) {
	h := newHandlerForStats(nil)
	rec := getStats(t, h)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "paywall_unavailable", body["code"], "code d'erreur attendu")
}

// 200 + body JSON conforme TestTenantsCleanupStats.
func TestHandleTestTenantsStats_Success(t *testing.T) {
	now := time.Now().UTC()
	stub := &fakeStatsProvider{
		stats: &service.TestTenantsCleanupStats{
			TotalTestTenants:      42,
			OrphansOlderThan1h:    12,
			LastAutoCleanupAt:     now,
			LastCleanupWipedCount: 5,
			Enabled:               true,
		},
	}
	h := newHandlerForStats(stub)
	rec := getStats(t, h)
	require.Equal(t, http.StatusOK, rec.Code)

	var got service.TestTenantsCleanupStats
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, 42, got.TotalTestTenants)
	assert.Equal(t, 12, got.OrphansOlderThan1h)
	assert.Equal(t, 5, got.LastCleanupWipedCount)
	assert.True(t, got.Enabled)
	assert.WithinDuration(t, now, got.LastAutoCleanupAt, time.Second)
}

// 200 + Enabled:false en mode prod (audit garde-fou).
func TestHandleTestTenantsStats_DisabledInProdReturns200WithEnabledFalse(t *testing.T) {
	stub := &fakeStatsProvider{
		stats: &service.TestTenantsCleanupStats{Enabled: false},
	}
	h := newHandlerForStats(stub)
	rec := getStats(t, h)
	require.Equal(t, http.StatusOK, rec.Code)

	var got service.TestTenantsCleanupStats
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.Enabled, "Enabled DOIT etre false si prod (audit garde-fou)")
}

// Erreur Stats() sans payload → 500.
func TestHandleTestTenantsStats_ErrorNoPartial500(t *testing.T) {
	stub := &fakeStatsProvider{err: errors.New("db down")}
	h := newHandlerForStats(stub)
	rec := getStats(t, h)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// Erreur Stats() + stats partielles → 500 avec body contenant partial_stats.
func TestHandleTestTenantsStats_ErrorWithPartial500(t *testing.T) {
	stub := &fakeStatsProvider{
		stats: &service.TestTenantsCleanupStats{Enabled: true, LastCleanupWipedCount: 7},
		err:   errors.New("workspace list timeout"),
	}
	h := newHandlerForStats(stub)
	rec := getStats(t, h)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	// WriteJSONErrorCode wrap dans "code" + "message" + "details" — verifions
	// que partial_stats est bien dans details (best-effort debug payload).
	if details, ok := body["details"].(map[string]interface{}); ok {
		assert.Contains(t, details, "partial_stats")
	} else {
		t.Logf("body=%v", body)
		t.Fatal("details map attendue contenant partial_stats")
	}
}
