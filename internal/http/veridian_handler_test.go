package http

// === Veridian patch ===
// Tests Go unitaires complets du VeridianHandler (7 endpoints + helpers).
// Couvre les transitions d'erreur (400/404/409/500), les bodies invalides,
// les sentinels (ErrTenantSoftDeleted), et le mapping HTTP.
//
// Avant ce fichier, le handler n'avait AUCUN test direct (couvert uniquement
// via e2e Playwright = lent + cher). Ces tests donnent un feedback ~1s vs
// 8min pour l'e2e.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
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

func newHandlerWithService(svc domain.VeridianService) *VeridianHandler {
	return &VeridianHandler{
		service: svc,
		logger:  logger.NewLogger(),
	}
}

// helper : POST avec body JSON, retourne la reponse decodee
func postJSON(t *testing.T, h func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := bytes.NewReader([]byte(body))
	req := httptest.NewRequest(http.MethodPost, path, r)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// helper : POST avec PathValue "id" simulé (pour /api/tenants/{id} routes)
func postWithPathValue(t *testing.T, h func(http.ResponseWriter, *http.Request), method, path, idValue, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.SetPathValue("id", idValue)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// === handleInvalidateCache (déjà existant) ===

func TestVeridianHandleInvalidateCache_OK(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)

	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate", `{"workspace_id":"ws-1"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["workspace_id"])
	assert.Equal(t, true, resp["invalidated"])
}

func TestVeridianHandleInvalidateCache_IdempotentOnUnknownWorkspace(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate", `{"workspace_id":"ws-doesnotexist"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleInvalidateCache_MissingWorkspaceID(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "workspace_id is required")
}

func TestVeridianHandleInvalidateCache_EmptyWorkspaceID(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate", `{"workspace_id":""}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleInvalidateCache_InvalidJSON(t *testing.T) {
	cache := middleware.NewPaywallCache()
	h := newHandlerWithCache(cache)
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate", `{not valid json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestVeridianHandleInvalidateCache_NoCacheReturns503(t *testing.T) {
	h := newHandlerWithCache(nil)
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate", `{"workspace_id":"ws-1"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "paywall cache not initialized")
}

// === handleProvision ===

func TestVeridianHandleProvision_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(&domain.ProvisionResponse{
		WorkspaceID: "ws-1",
		OwnerUserID: "u-1",
		APIKey:      "fake-jwt-token-here-long-enough",
		Created:     true,
	}, nil)

	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","owner_email":"o@x.test","plan":"free"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.ProvisionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.WorkspaceID)
	assert.True(t, resp.Created)
}

func TestVeridianHandleProvision_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl) // pas d'EXPECT — ne doit pas etre appele
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleProvision, "/api/tenants/provision", `{not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestVeridianHandleProvision_MissingTenantID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"owner_email":"o@x.test","plan":"free"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant_id and owner_email are required")
}

func TestVeridianHandleProvision_MissingOwnerEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","plan":"free"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleProvision_SoftDeletedReturns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	wrappedErr := fmt.Errorf("%w (deleted_at=...)", service.ErrTenantSoftDeleted)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(nil, wrappedErr)

	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","owner_email":"o@x.test","plan":"free"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "soft-deleted")
}

func TestVeridianHandleProvision_GenericServiceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(nil, errors.New("unexpected upstream"))

	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","owner_email":"o@x.test","plan":"free"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "unexpected upstream")
}

// === handleUpdatePlan ===

func TestVeridianHandleUpdatePlan_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), domain.UpdatePlanInput{TenantID: "ws-1", Plan: "pro"}).Return(nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-1","plan":"pro"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["tenant_id"])
	assert.Equal(t, "pro", resp["plan"])
	assert.NotNil(t, resp["applied_at"])
}

func TestVeridianHandleUpdatePlan_MissingFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	for _, body := range []string{`{}`, `{"tenant_id":"ws-1"}`, `{"plan":"pro"}`} {
		rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan", body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
	}
}

func TestVeridianHandleUpdatePlan_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).Return(errors.New("veridian_plan: workspace ws-x not found"))
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-x","plan":"pro"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVeridianHandleUpdatePlan_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan", `not json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === handleSuspend ===

func TestVeridianHandleSuspend_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Suspend(gomock.Any(), domain.SuspendInput{
		TenantID: "ws-1", Reason: "non-payment",
	}).Return(nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleSuspend, "/api/tenants/suspend",
		`{"tenant_id":"ws-1","reason":"non-payment"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["tenant_id"])
	assert.NotNil(t, resp["suspended_at"])
}

func TestVeridianHandleSuspend_MissingTenantID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleSuspend, "/api/tenants/suspend", `{"reason":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleSuspend_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Suspend(gomock.Any(), gomock.Any()).Return(sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleSuspend, "/api/tenants/suspend",
		`{"tenant_id":"ws-x","reason":"x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// === handleResume ===

func TestVeridianHandleResume_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Resume(gomock.Any(), domain.ResumeInput{TenantID: "ws-1"}).Return(nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleResume, "/api/tenants/resume", `{"tenant_id":"ws-1"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleResume_MissingTenantID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleResume, "/api/tenants/resume", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleResume_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Resume(gomock.Any(), gomock.Any()).Return(errors.New("veridian_plan: workspace not found"))
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleResume, "/api/tenants/resume", `{"tenant_id":"ws-x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// === handleDelete (DELETE /api/tenants/{id}) ===

func TestVeridianHandleDelete_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SoftDelete(gomock.Any(), "ws-1").Return(nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleDelete, http.MethodDelete, "/api/tenants/ws-1", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["tenant_id"])
	assert.NotNil(t, resp["deleted_at"])
}

func TestVeridianHandleDelete_MissingID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleDelete, http.MethodDelete, "/api/tenants/", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleDelete_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SoftDelete(gomock.Any(), "ws-x").Return(sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleDelete, http.MethodDelete, "/api/tenants/ws-x", "ws-x", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// === handleStatus (GET /api/tenants/{id}/status) ===

func TestVeridianHandleStatus_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GetStatus(gomock.Any(), "ws-1").Return(&domain.StatusResponse{
		TenantID:            "ws-1",
		Status:              domain.PlanStatusActive,
		Plan:                "pro",
		MonthlyEmailQuota:   10000,
		EmailsSentThisMonth: 100,
		QuotaRemaining:      9900,
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleStatus, http.MethodGet, "/api/tenants/ws-1/status", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.StatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, domain.PlanStatusActive, resp.Status)
	assert.Equal(t, int64(9900), resp.QuotaRemaining)
}

func TestVeridianHandleStatus_MissingID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleStatus, http.MethodGet, "/api/tenants//status", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleStatus_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GetStatus(gomock.Any(), "ws-x").Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleStatus, http.MethodGet, "/api/tenants/ws-x/status", "ws-x", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// === handleWipeTestTenants ===

func TestVeridianHandleWipeTestTenants_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().WipeTestTenants(gomock.Any(), gomock.Any()).Return(&domain.WipeTestTenantsResponse{
		Wiped:   []string{"chaos-1", "chaos-2"},
		Skipped: []string{},
		Errors:  map[string]string{},
	}, nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleWipeTestTenants, "/api/veridian/admin/wipe-test-tenants",
		`{"prefix":"chaos"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.WipeTestTenantsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Wiped, 2)
}

func TestVeridianHandleWipeTestTenants_MissingPrefixAndIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleWipeTestTenants, "/api/veridian/admin/wipe-test-tenants", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "prefix or tenant_ids required")
}

func TestVeridianHandleWipeTestTenants_TenantIDsOnly(t *testing.T) {
	// Prefix vide mais tenant_ids non vides → OK (validation passe)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().WipeTestTenants(gomock.Any(), gomock.Any()).Return(&domain.WipeTestTenantsResponse{
		Wiped: []string{"abc-123"},
	}, nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleWipeTestTenants, "/api/veridian/admin/wipe-test-tenants",
		`{"tenant_ids":["abc-123"]}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleWipeTestTenants_ServiceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().WipeTestTenants(gomock.Any(), gomock.Any()).Return(nil,
		errors.New("prefix cannot contain SQL wildcards (% or _)"))
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleWipeTestTenants, "/api/veridian/admin/wipe-test-tenants",
		`{"prefix":"%"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "wildcards")
}

func TestVeridianHandleWipeTestTenants_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleWipeTestTenants, "/api/veridian/admin/wipe-test-tenants", `garbage{`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// === isNotFoundErr helper ===

func TestVeridianIsNotFoundErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"sql.ErrNoRows direct", sql.ErrNoRows, true},
		{"wrapped sql.ErrNoRows", fmt.Errorf("wrap: %w", sql.ErrNoRows), true},
		{"message contains 'not found'", errors.New("workspace ws-1 not found"), true},
		{"message contains 'ErrNoRows'", errors.New("repo: ErrNoRows on lookup"), true},
		{"unrelated error", errors.New("connection refused"), false},
		{"empty message", errors.New(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNotFoundErr(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// === containsAny helper (tests indirects, mais utile pour confidence) ===

func TestVeridianContainsAny(t *testing.T) {
	assert.True(t, containsAny("hello world", "world"))
	assert.True(t, containsAny("hello world", "no", "world"))
	assert.False(t, containsAny("hello world", "xxx"))
	assert.False(t, containsAny("hello world"))            // no needles
	assert.True(t, containsAny("aaa", "", "a"))            // empty skipped, then match
	assert.True(t, containsAny("404 page not found", "not found"))
}

// === RegisterRoutes : verifie que les 8 routes sont bien attachées ===

func TestVeridianRegisterRoutes_AllPathsRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := NewVeridianHandler(svc, logger.NewLogger())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret")

	// On verifie que pour chaque route, mux.Handler ne tombe PAS sur une 404
	// (sinon la route n'a pas ete enregistree). On utilise le mux.Handler pour
	// recuperer le handler associe et verifier qu'il n'est pas le default 404.
	pathsToVerify := []struct {
		method, path string
	}{
		{"POST", "/api/tenants/provision"},
		{"POST", "/api/tenants/update-plan"},
		{"POST", "/api/tenants/suspend"},
		{"POST", "/api/tenants/resume"},
		{"DELETE", "/api/tenants/ws-1"},
		{"GET", "/api/tenants/ws-1/status"},
		{"POST", "/api/veridian/admin/wipe-test-tenants"},
		{"POST", "/api/veridian/admin/cache/invalidate"},
	}

	for _, p := range pathsToVerify {
		req := httptest.NewRequest(p.method, p.path, nil)
		_, pattern := mux.Handler(req)
		assert.NotEmpty(t, pattern, "route %s %s should be registered", p.method, p.path)
	}
}

// Test que sans HUB_API_SECRET (secret vide), les routes sont quand meme
// enregistrees mais le middleware HMAC retourne 503.
func TestVeridianRegisterRoutes_EmptyHubSecretReturns503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := NewVeridianHandler(svc, logger.NewLogger())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "") // secret vide

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision",
		bytes.NewReader([]byte(`{"tenant_id":"x","owner_email":"y@z"}`)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// === SetPaywallCache : cache opt-in workflow ===

func TestVeridianSetPaywallCache_NilByDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := NewVeridianHandler(svc, logger.NewLogger())

	// Avant SetPaywallCache : invalidate doit retourner 503
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate",
		`{"workspace_id":"ws-1"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	// Apres SetPaywallCache : invalidate doit retourner 200
	cache := middleware.NewPaywallCache()
	h.SetPaywallCache(cache)
	rec2 := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate",
		`{"workspace_id":"ws-1"}`)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

// Anti-regression : applied_at, suspended_at, etc. doivent etre des timestamps
// valides RFC3339 (parseable par time.Parse). Sinon le client TS aura du mal.
func TestVeridianHandleSuspend_ReturnsValidTimestamp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Suspend(gomock.Any(), gomock.Any()).Return(nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleSuspend, "/api/tenants/suspend",
		`{"tenant_id":"ws-1","reason":"x"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	tsStr, ok := resp["suspended_at"].(string)
	require.True(t, ok, "suspended_at must be string")
	_, err := time.Parse(time.RFC3339, tsStr)
	assert.NoError(t, err, "suspended_at must be RFC3339")
}

// === handleMode (Veridian-managed mode detection for console UI) ===

func TestVeridianHandleMode_VeridianManaged(t *testing.T) {
	h := newHandlerWithService(nil)
	handler := h.handleMode("some-hub-secret-set")

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/mode", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "veridian-managed", got["mode"])
	require.Equal(t, "/console/signin", got["signin_url"])
	require.Equal(t, "https://app.veridian.site", got["hub_url"])
}

func TestVeridianHandleMode_SelfHosted(t *testing.T) {
	h := newHandlerWithService(nil)
	handler := h.handleMode("") // HUB_API_SECRET vide

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/mode", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "self-hosted", got["mode"])
	require.Equal(t, "/console/signin", got["signin_url"])
	_, hasHubURL := got["hub_url"]
	require.False(t, hasHubURL, "self-hosted mode should not expose hub_url")
}

func TestVeridianHandleMode_NoAuthRequired(t *testing.T) {
	// L'endpoint /api/veridian/mode est public (pas de HMAC, pas de auth).
	// On verifie qu'aucun header n'est requis.
	h := newHandlerWithService(nil)
	handler := h.handleMode("hub-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/mode", nil)
	// pas de X-Veridian-Hub-Signature, pas de Authorization
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "endpoint public, no auth required")
}

// === handleAttachOwner ===
// Cf. todo/2026-05-17-provision-owner-attach.md — endpoint réparateur P0.

func TestVeridianHandleAttachOwner_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachOwner(gomock.Any(), domain.AttachOwnerInput{
		TenantID:   "robertbrunon",
		OwnerEmail: "robert.brunon@veridian.site",
	}).Return(&domain.AttachOwnerResponse{
		TenantID:         "robertbrunon",
		OwnerEmail:       "robert.brunon@veridian.site",
		UserID:           "0cb49456-12cc-43f2-9a4e-423d16fcfb44",
		Attached:         true,
		AlreadyAttached:  false,
		OwnerTransferred: true,
	}, nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleAttachOwner, "/api/veridian/admin/attach-owner",
		`{"tenant_id":"robertbrunon","owner_email":"robert.brunon@veridian.site"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp domain.AttachOwnerResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Attached)
	assert.True(t, resp.OwnerTransferred)
	assert.Equal(t, "0cb49456-12cc-43f2-9a4e-423d16fcfb44", resp.UserID)
}

func TestVeridianHandleAttachOwner_MissingFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	// tenant_id manquant
	rec := postJSON(t, h.handleAttachOwner, "/api/veridian/admin/attach-owner",
		`{"owner_email":"x@y.z"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "tenant_id and owner_email are required")

	// owner_email manquant
	rec = postJSON(t, h.handleAttachOwner, "/api/veridian/admin/attach-owner",
		`{"tenant_id":"ws-1"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleAttachOwner_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleAttachOwner, "/api/veridian/admin/attach-owner", `not-json`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleAttachOwner_ServiceError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachOwner(gomock.Any(), gomock.Any()).Return(nil,
		errors.New("transfer ownership to alice@x: workspace not found"))
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleAttachOwner, "/api/veridian/admin/attach-owner",
		`{"tenant_id":"ghost","owner_email":"alice@x"}`)
	// "not found" dans message → 404 via isNotFoundErr
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVeridianHandleAttachOwner_RegisteredInRoutes(t *testing.T) {
	// Garantit que la route est bien câblée dans le mux (Constitution §1 :
	// chaque route déclarée doit être exercée par un test).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/attach-owner", nil)
	matched, pattern := mux.Handler(req)
	require.NotNil(t, matched)
	assert.Contains(t, pattern, "/api/veridian/admin/attach-owner")
}
