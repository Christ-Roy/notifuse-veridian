package middleware

// Tests Lot J — Mode dégradé soft-deleted.
// Couvre :
//   - obfuscation des reads quand tenant deleted_at != NULL
//   - 402 standardisé sur les writes (tenant_soft_deleted body)
//   - exempts /api/veridian/* (admin Hub) et /api/tenants/* (HMAC)
//   - extraction workspace_id depuis query string OU body
//   - cohabitation avec Lot H HubSyncStatus : soft-deleted prime
//   - SENSITIVE_FIELDS toujours full obfusqué

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deletedPlan retourne un plan soft-deleted (deleted_at à -1h, purge_eligible_at à +29j).
func deletedPlan(workspaceID string) *domain.VeridianPlan {
	deletedAt := time.Now().Add(-1 * time.Hour)
	purgeAt := time.Now().Add(29 * 24 * time.Hour)
	return &domain.VeridianPlan{
		WorkspaceID:     workspaceID,
		Plan:            "pro",
		Status:          domain.PlanStatusDeleted,
		DeletedAt:       &deletedAt,
		PurgeEligibleAt: &purgeAt,
	}
}

// activePlan retourne un plan actif (pas soft-deleted).
func activePlan(workspaceID string) *domain.VeridianPlan {
	return &domain.VeridianPlan{
		WorkspaceID:       workspaceID,
		Plan:              "pro",
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: 10000,
	}
}

// jsonHandler retourne un handler qui répond JSON (utilisé comme handler
// upstream que le middleware va wrapper).
func jsonHandler(payload interface{}, called *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if called != nil {
			called.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	})
}

// === Routes lecture : obfuscation ===

func TestVeridianSoftDeleted_ReadGET_QueryString_Obfuscated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-deleted").Return(deletedPlan("ws-deleted"), nil).Times(1)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{
		"name":     "Alice Wonderland",
		"email":    "alice@example.com",
		"password": "supersecret",
		"count":    42,
	}, &called)

	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-deleted", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load(), "handler upstream doit avoir été appelé")

	// Headers UI bandeau
	assert.Equal(t, "true", rec.Header().Get("X-Tenant-Soft-Deleted"))
	assert.NotEmpty(t, rec.Header().Get("X-Tenant-Deleted-At"))
	assert.NotEmpty(t, rec.Header().Get("X-Tenant-Purge-At"))

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	// "Alice Wonderland" = 16 chars → 5 keep
	assert.Equal(t, "Alice•••••••••••", got["name"])
	// email = 17 chars → 5 keep
	assert.Equal(t, "alice••••••••••••", got["email"])
	// password = SENSITIVE_FIELD → full
	assert.Equal(t, "•••••••••••", got["password"])
	// number préservé
	assert.EqualValues(t, 42, got["count"])
}

func TestVeridianSoftDeleted_ReadGET_NoWorkspaceIDPassThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// repo.Get jamais appelé (pas de workspace_id à lookup)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"name": "Original"}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/something.public", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())

	// Pas d'obfuscation : body original
	assert.Contains(t, rec.Body.String(), "Original")
}

func TestVeridianSoftDeleted_ReadGET_ActiveTenantPassThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-active").Return(activePlan("ws-active"), nil).Times(1)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"name": "Alice"}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-active", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
	// Pas d'obfuscation : tenant actif
	assert.Empty(t, rec.Header().Get("X-Tenant-Soft-Deleted"))
	assert.Contains(t, rec.Body.String(), "Alice")
}

func TestVeridianSoftDeleted_NonJSONReadPassThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-deleted").Return(deletedPlan("ws-deleted"), nil).Times(1)

	// Upstream renvoie du CSV (non-JSON) : doit passer through sans obfuscation
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("name,email\nAlice,alice@example.com\n"))
	})
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.export?workspace_id=ws-deleted", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "alice@example.com", "CSV non-obfuscated (passe-through)")
}

// === Routes écriture : 402 tenant_soft_deleted ===

func TestVeridianSoftDeleted_WritePOST_Returns402(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-deleted").Return(deletedPlan("ws-deleted"), nil).Times(1)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"ok": true}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodPost, "/api/contacts.upsert",
		strings.NewReader(`{"workspace_id":"ws-deleted","contact":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.False(t, called.Load(), "handler upstream NE DOIT PAS être appelé")

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "tenant_soft_deleted", body["error"])
	assert.Equal(t, "tenant_soft_deleted", body["error_code"])
	assert.Contains(t, body["restore_url"], "/dashboard?action=restore&tenant=ws-deleted")
	assert.NotEmpty(t, body["deleted_at"])
	assert.NotEmpty(t, body["purge_eligible_at"])
}

func TestVeridianSoftDeleted_WriteAllMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			repo := mocks.NewMockVeridianPlanRepository(ctrl)
			repo.EXPECT().Get(gomock.Any(), "ws-deleted").Return(deletedPlan("ws-deleted"), nil).Times(1)

			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("handler upstream NE DOIT PAS être appelé pour %s", method)
			})
			mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

			req := httptest.NewRequest(method, "/api/contacts.delete?workspace_id=ws-deleted", nil)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusPaymentRequired, rec.Code, "method=%s", method)
		})
	}
}

// === Exempts : /api/veridian/* et /api/tenants/* ===

func TestVeridianSoftDeleted_AdminHubRoute_Exempted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// Aucun appel attendu : exemption AVANT lookup

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"ok": true}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/restore",
		strings.NewReader(`{"workspace_id":"ws-deleted"}`))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "admin Hub doit passer même si tenant deleted")
	assert.True(t, called.Load())
}

func TestVeridianSoftDeleted_HMACTenantsRoute_Exempted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"ok": true}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-deleted/restore",
		strings.NewReader(`{"workspace_id":"ws-deleted"}`))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "HMAC tenants doit passer (Hub manage)")
	assert.True(t, called.Load())
}

func TestVeridianSoftDeleted_HealthAndVersion_Exempted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	upstream := jsonHandler(map[string]interface{}{"ok": true}, nil)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	for _, path := range []string{"/api/health", "/api/version", "/api/auth/login"} {
		req := httptest.NewRequest(http.MethodGet, path+"?workspace_id=ws-deleted", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "path %s doit être exempté", path)
	}
}

// === workspace_id extraction ===

func TestVeridianSoftDeleted_WorkspaceIDInBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-from-body").Return(deletedPlan("ws-from-body"), nil).Times(1)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler upstream NE DOIT PAS être appelé (write deleted)")
	})
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	// Pas de query string, workspace_id uniquement dans le body
	req := httptest.NewRequest(http.MethodPost, "/api/contacts.upsert",
		strings.NewReader(`{"workspace_id":"ws-from-body","contact":{"email":"x@y.com"}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
}

func TestVeridianSoftDeleted_BodyRebufferedForHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-rebuf").Return(activePlan("ws-rebuf"), nil).Times(1)

	expectedBody := `{"workspace_id":"ws-rebuf","extra":"data"}`
	var gotBody string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, len(expectedBody))
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	})

	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodPost, "/api/foo",
		strings.NewReader(expectedBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, expectedBody, gotBody, "handler upstream doit voir le même body")
}

// === DB error fail-open ===

func TestVeridianSoftDeleted_DBErrorFailsOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-dberror").Return(nil, assertErr("conn refused")).Times(1)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"data": "in clear"}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-dberror", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "DB error must fail-open")
	assert.True(t, called.Load())
	assert.Contains(t, rec.Body.String(), "in clear", "fail-open : pas d'obfuscation")
}

// === Plan absent : passe-through ===

func TestVeridianSoftDeleted_PlanAbsent_PassThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// sql.ErrNoRows
	repo.EXPECT().Get(gomock.Any(), "ws-self-hosted").Return(nil, sqlNoRows()).Times(1)

	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"data": "self-hosted"}, &called)
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-self-hosted", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
	assert.Contains(t, rec.Body.String(), "self-hosted", "self-hosted tenant : pas d'obfuscation")
}

// === Nil repo : passthrough ===

func TestVeridianSoftDeleted_NilRepo_Passthrough(t *testing.T) {
	var called atomic.Bool
	upstream := jsonHandler(map[string]interface{}{"data": "ok"}, &called)
	mw := NewVeridianSoftDeletedMiddleware(nil, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodGet, "/api/foo?workspace_id=ws-x", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
}

// === Cache partagé : invalidate marche aussi sur le middleware soft-deleted ===

func TestVeridianSoftDeleted_SharedCacheInvalidate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// 1er call : plan deleted → obfuscation
	repo.EXPECT().Get(gomock.Any(), "ws-shared").Return(deletedPlan("ws-shared"), nil).Times(1)
	// 2e call après Invalidate : plan active → passe-through
	repo.EXPECT().Get(gomock.Any(), "ws-shared").Return(activePlan("ws-shared"), nil).Times(1)

	cache := NewPaywallCache()
	upstream := jsonHandler(map[string]interface{}{"name": "Alice"}, nil)
	mw := NewVeridianSoftDeletedMiddlewareWithCache(cache, repo, logger.NewLogger())(upstream)

	// 1er passage : deleted
	req1 := httptest.NewRequest(http.MethodGet, "/api/foo?workspace_id=ws-shared", nil)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)
	assert.Equal(t, "true", rec1.Header().Get("X-Tenant-Soft-Deleted"))

	// 2e passage SANS invalidate : cache hit, toujours deleted
	req2 := httptest.NewRequest(http.MethodGet, "/api/foo?workspace_id=ws-shared", nil)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	assert.Equal(t, "true", rec2.Header().Get("X-Tenant-Soft-Deleted"))

	// Invalidate → fresh lookup
	cache.Invalidate("ws-shared")

	// 3e passage : plan active, pas d'obfuscation
	req3 := httptest.NewRequest(http.MethodGet, "/api/foo?workspace_id=ws-shared", nil)
	rec3 := httptest.NewRecorder()
	mw.ServeHTTP(rec3, req3)
	assert.Empty(t, rec3.Header().Get("X-Tenant-Soft-Deleted"))
	assert.Contains(t, rec3.Body.String(), "Alice")
}

// === Cohabitation avec Lot H : soft-deleted prime sur HubSyncDead (paywall middleware) ===
//
// Le test TestVeridianPaywall_HubSyncDead_SoftDeletedPrimes valide déjà
// cette priorité côté paywall middleware. Ici on vérifie côté soft-deleted
// global : un tenant deleted ET dead → on retourne 402 tenant_soft_deleted
// (pas 503 hub_sync_dead, qui est le domaine du paywall).
func TestVeridianSoftDeleted_PrimesOverHubSyncDead(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	deletedAt := time.Now().Add(-1 * time.Hour)
	syncAt := time.Now().Add(-73 * time.Hour) // dead
	plan := &domain.VeridianPlan{
		WorkspaceID:   "ws-both",
		Plan:          "pro",
		Status:        domain.PlanStatusDeleted,
		DeletedAt:     &deletedAt,
		LastHubSyncAt: &syncAt,
	}
	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-both").Return(plan, nil).Times(1)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream NE DOIT PAS être appelé (write + deleted)")
	})
	mw := NewVeridianSoftDeletedMiddleware(repo, logger.NewLogger())(upstream)

	req := httptest.NewRequest(http.MethodPost, "/api/contacts.upsert",
		strings.NewReader(`{"workspace_id":"ws-both"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code,
		"soft-deleted prime sur hub_sync_dead → 402 (pas 503)")
	assert.Contains(t, rec.Body.String(), "tenant_soft_deleted")
	assert.NotContains(t, rec.Body.String(), "hub_sync_dead",
		"hub_sync_dead code ne doit pas apparaître quand soft-deleted")
}

// === Tests helpers internes ===

func TestExtractWorkspaceIDForSoftDelete_PriorityQueryOverBody(t *testing.T) {
	// Query string a la priorité sur le body.
	req := httptest.NewRequest(http.MethodPost,
		"/api/foo?workspace_id=from-query",
		strings.NewReader(`{"workspace_id":"from-body"}`))
	wid, ok := extractWorkspaceIDForSoftDelete(req)
	assert.True(t, ok)
	assert.Equal(t, "from-query", wid)
}

func TestExtractWorkspaceIDForSoftDelete_GETWithoutQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/foo", nil)
	_, ok := extractWorkspaceIDForSoftDelete(req)
	assert.False(t, ok)
}

func TestIsSoftDeletedExemptPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/veridian/admin/cache/invalidate", true},
		{"/api/veridian/", true},
		{"/api/tenants/abc/restore", true},
		{"/api/tenants/", true},
		{"/api/health", true},
		{"/api/version", true},
		{"/api/auth/login", true},
		// Non-exempts
		{"/api/contacts.list", false},
		{"/api/transactional.send", false},
		{"/api/broadcasts.create", false},
		{"/api/tenant.profile", false}, // pas /api/tenants/ (singulier)
		{"/api/veridia/foo", false},     // typo : pas /api/veridian/
		{"/", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, isSoftDeletedExemptPath(tc.path))
		})
	}
}

func TestIsSoftDeletedWrite(t *testing.T) {
	cases := map[string]bool{
		http.MethodGet:     false,
		http.MethodHead:    false,
		http.MethodOptions: false,
		http.MethodPost:    true,
		http.MethodPut:     true,
		http.MethodPatch:   true,
		http.MethodDelete:  true,
	}
	for method, want := range cases {
		assert.Equal(t, want, isSoftDeletedWrite(method), "method %s", method)
	}
}

// === Helpers test ===

type assertErr string

func (e assertErr) Error() string { return string(e) }

func sqlNoRows() error {
	return sql.ErrNoRows
}
