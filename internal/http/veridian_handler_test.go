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

// newHandlerWithServiceAndCache combine un service mocké + un PaywallCache
// injecté. Utilisé par les tests d'invalidation cache post-mutation (audit
// trial résidus 2026-05-24) qui doivent observer que le cache est purgé
// après UpdatePlan / Resume / Suspend / SoftDelete / Restore.
func newHandlerWithServiceAndCache(svc domain.VeridianService, cache *middleware.PaywallCache) *VeridianHandler {
	h := &VeridianHandler{
		service: svc,
		logger:  logger.NewLogger(),
	}
	if cache != nil {
		h.SetPaywallCache(cache)
	}
	return h
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

// === Veridian patch === Sentinel ErrOwnerMismatch → 409 Conflict.
// Contrat §5.1 : re-provision avec owner_email different doit etre refusee
// pour empecher la prise de controle d'un tenant existant via magic_link
// regenere (ticket Hub 2026-05-18-confirm-provision-idempotence).
func TestVeridianHandleProvision_OwnerMismatchReturns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(nil, service.ErrOwnerMismatch)

	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-shared","owner_email":"mallory@x.test","plan":"free"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "different owner")
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
	svc.EXPECT().UpdatePlan(gomock.Any(), domain.UpdatePlanInput{TenantID: "ws-1", Plan: "pro"}).
		Return(&domain.UpdatePlanResponse{
			TenantID:     "ws-1",
			Plan:         "pro",
			PreviousPlan: "free",
			PlanSource:   domain.PlanSourceStripe,
			AppliedAt:    time.Now().UTC(),
		}, nil)
	h := newHandlerWithService(svc)

	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-1","plan":"pro"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["tenant_id"])
	assert.Equal(t, "pro", resp["plan"])
	assert.Equal(t, "free", resp["previous_plan"])
	assert.Equal(t, "stripe", resp["plan_source"])
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
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).Return(nil, errors.New("veridian_plan: workspace ws-x not found"))
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

// === CONTRAT-BILLING v2 — conformite du handler update-plan ===
func TestVeridianHandleUpdatePlan_V2_UnsupportedContractVersionMajor(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"contract_version":"3.0","tenant_id":"ws-1","plan":"pro"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeInvalidPayload, body.Code)
	assert.Equal(t, "3.0", body.Details["contract_version"])
}

func TestVeridianHandleUpdatePlan_V2_AcceptsContractVersionMinorBump(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).
		Return(&domain.UpdatePlanResponse{TenantID: "ws-1", Plan: "pro", AppliedAt: time.Now().UTC()}, nil)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"contract_version":"2.99","tenant_id":"ws-1","plan":"pro"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleUpdatePlan_V2_AcceptsLegacyV1NoContractVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).
		Return(&domain.UpdatePlanResponse{TenantID: "ws-1", Plan: "free", AppliedAt: time.Now().UTC()}, nil)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-1","plan":"free"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleUpdatePlan_V2_InvalidPlan_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"contract_version":"2.0","tenant_id":"ws-1","plan":"freemium"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeInvalidPlan, body.Code)
	assert.Equal(t, "freemium", body.Details["plan"])
}

func TestVeridianHandleUpdatePlan_V2_AcceptsNewPlanSourceValues(t *testing.T) {
	for _, src := range []string{"stripe_trial", "grant_manual", "downgrade_auto"} {
		t.Run(src, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			svc := mocks.NewMockVeridianService(ctrl)
			svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).
				Return(&domain.UpdatePlanResponse{TenantID: "ws-1", Plan: "pro", AppliedAt: time.Now().UTC()}, nil)
			h := newHandlerWithService(svc)
			body := `{"contract_version":"2.0","tenant_id":"ws-1","plan":"pro","plan_source":"` + src + `"}`
			rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan", body)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

func TestVeridianHandleUpdatePlan_V2_RejectsUnknownPlanSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"contract_version":"2.0","tenant_id":"ws-1","plan":"pro","plan_source":"garbage"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeInvalidPayload, body.Code)
	assert.Equal(t, "garbage", body.Details["plan_source"])
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
	// Le handler DELETE legacy delegue maintenant a service.SoftDelete avec un
	// SoftDeleteInput{TenantID: id, Reason: ""}.
	svc.EXPECT().SoftDelete(gomock.Any(), domain.SoftDeleteInput{TenantID: "ws-1"}).
		Return(&domain.SoftDeleteResponse{
			TenantID:        "ws-1",
			Status:          "deleted",
			DeletedAt:       time.Now().UTC(),
			PurgeEligibleAt: time.Now().UTC().Add(30 * 24 * time.Hour),
		}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleDelete, http.MethodDelete, "/api/tenants/ws-1", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp["tenant_id"])
	assert.NotNil(t, resp["deleted_at"])
	assert.NotNil(t, resp["purge_eligible_at"], "purge_eligible_at maintenant inclus dans response legacy DELETE")
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
	svc.EXPECT().SoftDelete(gomock.Any(), domain.SoftDeleteInput{TenantID: "ws-x"}).
		Return(nil, sql.ErrNoRows)
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
	assert.False(t, containsAny("hello world")) // no needles
	assert.True(t, containsAny("aaa", "", "a")) // empty skipped, then match
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
		// === Veridian patch 2026-05-24 — cron auto-cleanup orphans staging ===
		{"GET", "/api/veridian/admin/test-tenants-stats"},
	}

	for _, p := range pathsToVerify {
		req := httptest.NewRequest(p.method, p.path, nil)
		_, pattern := mux.Handler(req)
		assert.NotEmpty(t, pattern, "route %s %s should be registered", p.method, p.path)
	}
}

// === Veridian patch 2026-05-24 — cron auto-cleanup orphans staging ===
// Test que SetTestTenantsCleanup (nil par défaut) entraîne 503 sur l'endpoint
// GET /api/veridian/admin/test-tenants-stats — garde-fou prod : un agent qui
// curl en prod doit voir 503 explicit, jamais un 200 muet.
func TestVeridianSetTestTenantsCleanup_NilByDefaultReturns503(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := NewVeridianHandler(svc, logger.NewLogger())

	// Sans setter, le handler doit retourner 503.
	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/test-tenants-stats", nil)
	rec := httptest.NewRecorder()
	h.handleTestTenantsStats(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"sans SetTestTenantsCleanup, l'endpoint stats doit retourner 503")
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

// === handleVersion (build-time injected tag + sha) ===
// Cf. internal/buildinfo + Dockerfile ARG BUILD_TAG/BUILD_SHA/BUILD_DATE.
// L'endpoint sert au step "Verify prod runs new code" du workflow CI pour
// détecter qu'un redeploy a effectivement remplacé le container.

func TestVeridianHandleVersion_DefaultsDev(t *testing.T) {
	// En test (sans ldflags), les 3 vars buildinfo restent à "dev".
	h := newHandlerWithService(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()
	h.handleVersion(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "dev", got["tag"])
	require.Equal(t, "dev", got["git_sha"])
	require.Equal(t, "dev", got["build_date"])
}

func TestVeridianHandleVersion_PublicNoAuth(t *testing.T) {
	// Endpoint public — pas de HMAC, pas de header requis.
	h := newHandlerWithService(nil)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()
	h.handleVersion(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "no auth required for /api/version")
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

// === handleHealth (livrable 3 contrat intégrations Hub) ===

func TestVeridianHandleHealth_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Health(gomock.Any(), "ws-h").Return(&domain.TenantHealthResponse{
		TenantID: "ws-h", WorkspaceID: "ws-h",
		Status:        domain.PlanStatusActive,
		OwnerAttached: true, OwnerEmail: "owner@x.test", OwnerUserID: "owner-id",
		APIKeyValid: true, MagicLinkCapable: true, MembersCount: 2, Plan: "free",
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleHealth, http.MethodGet, "/api/tenants/ws-h/health", "ws-h", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp domain.TenantHealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-h", resp.TenantID)
	assert.True(t, resp.MagicLinkCapable)
	assert.Equal(t, 2, resp.MembersCount)
}

func TestVeridianHandleHealth_MissingID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleHealth, http.MethodGet, "/api/tenants//health", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleHealth_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Health(gomock.Any(), "ws-x").Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleHealth, http.MethodGet, "/api/tenants/ws-x/health", "ws-x", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVeridianHandleHealth_BugDetectionReturnsOK(t *testing.T) {
	// Health renvoie 200 même si magic_link_capable=false — c'est le rôle du
	// Hub de scanner le champ et déclencher une alerte. On veut juste vérifier
	// que le handler n'interprète pas un état "cassé" comme une erreur 5xx.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Health(gomock.Any(), "ws-bug").Return(&domain.TenantHealthResponse{
		TenantID: "ws-bug", WorkspaceID: "ws-bug",
		Status:        domain.PlanStatusActive,
		OwnerAttached: false, APIKeyValid: true,
		MagicLinkCapable: false, MembersCount: 1,
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleHealth, http.MethodGet, "/api/tenants/ws-bug/health", "ws-bug", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.TenantHealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.MagicLinkCapable)
}

func TestVeridianHandleHealth_RegisteredInRoutes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret")

	req := httptest.NewRequest(http.MethodGet, "/api/tenants/ws-1/health", nil)
	matched, pattern := mux.Handler(req)
	require.NotNil(t, matched)
	assert.Contains(t, pattern, "/health")
}

// === Format d'erreur §5.10 — verification du champ `code` machine ===
//
// Couvre tous les chemins d'erreur des handlers Veridian pour s'assurer que
// le champ `code` machine-readable est emis en parallele de `error` (humain).
// Le contrat veut a terme un champ `code` strict que le Hub puisse switcher
// dessus sans parser la chaine humaine.

func decodeErrCode(t *testing.T, body []byte) string {
	t.Helper()
	var resp VeridianErrorResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Code
}

func TestVeridianErrorCode_handleProvision_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision", `{garbage`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleProvision_MissingFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
	// Verifie que le champ `details.missing` est rempli avec les champs manquants.
	var resp VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Details, "details doit etre present quand des champs manquent")
	missing, ok := resp.Details["missing"].([]interface{})
	require.True(t, ok, "details.missing doit etre un tableau")
	assert.ElementsMatch(t, []interface{}{"tenant_id", "owner_email"}, missing)
}

func TestVeridianErrorCode_handleProvision_OwnerMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(nil, service.ErrOwnerMismatch)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","owner_email":"intruder@x.test","plan":"free"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ErrCodeOwnerMismatch, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleProvision_SoftDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(nil, service.ErrTenantSoftDeleted)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","owner_email":"o@x.test","plan":"free"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ErrCodeTenantSoftDeleted, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleProvision_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Provision(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision",
		`{"tenant_id":"ws-1","owner_email":"o@x.test","plan":"free"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, ErrCodeInternalError, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleUpdatePlan_TenantNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ghost","plan":"pro"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, ErrCodeTenantNotFound, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleUpdatePlan_PlanLocked(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).Return(nil, service.ErrPlanImmune)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-vip","plan":"free","plan_source":"stripe"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ErrCodePlanLocked, decodeErrCode(t, rec.Body.Bytes()))
	// details : tenant_id, requested_plan, hint
	var resp VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Details)
	assert.Equal(t, "ws-vip", resp.Details["tenant_id"])
	assert.Equal(t, "free", resp.Details["requested_plan"])
	assert.NotEmpty(t, resp.Details["hint"])
}

func TestVeridianErrorCode_handleUpdatePlan_InvalidPlanSource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	// PAS d'EXPECT : la validation handler court-circuite avant le service.
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-1","plan":"pro","plan_source":"garbage"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
	var resp VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Details)
	assert.Equal(t, "garbage", resp.Details["plan_source"])
}

func TestVeridianErrorCode_handleSuspend_MissingTenantID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postJSON(t, h.handleSuspend, "/api/tenants/suspend", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleResume_TenantNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Resume(gomock.Any(), gomock.Any()).Return(fmt.Errorf("veridian_plan: workspace ghost not found"))
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleResume, "/api/tenants/resume", `{"tenant_id":"ghost"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, ErrCodeTenantNotFound, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleDelete_MissingID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postWithPathValue(t, h.handleDelete, http.MethodDelete, "/api/tenants/", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleStatus_TenantNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GetStatus(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)
	rec := postWithPathValue(t, h.handleStatus, http.MethodGet, "/api/tenants/ghost/status", "ghost", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, ErrCodeTenantNotFound, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleAttachOwner_OwnerMismatch(t *testing.T) {
	// Sentinel non-typee : on simule un not-found dans le service.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().AttachOwner(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)
	rec := postJSON(t, h.handleAttachOwner, "/api/veridian/admin/attach-owner",
		`{"tenant_id":"ghost","owner_email":"alice@x.test"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, ErrCodeTenantNotFound, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleHealth_MissingID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postWithPathValue(t, h.handleHealth, http.MethodGet, "/api/tenants//health", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleInvalidateCache_PaywallUnavailable(t *testing.T) {
	h := newHandlerWithCache(nil)
	rec := postJSON(t, h.handleInvalidateCache, "/api/veridian/admin/cache/invalidate",
		`{"workspace_id":"ws-1"}`)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, ErrCodePaywallUnavailable, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianErrorCode_handleWipeTestTenants_InvalidPayload(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postJSON(t, h.handleWipeTestTenants, "/api/veridian/admin/wipe-test-tenants", `{}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

// Sanity backward-compat : le champ `error` reste lisible (le Hub le rebalance
// aux clients comme message humain via NotifuseError, cf. veridian-hub/lib/
// notifuse/client.ts:243).
func TestVeridianErrorCode_BackwardCompat_ErrorFieldStillHumanMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	h := newHandlerWithService(mocks.NewMockVeridianService(ctrl))
	rec := postJSON(t, h.handleProvision, "/api/tenants/provision", `{}`)
	var resp VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Error, "champ 'error' doit rester non-vide pour retro-compat Hub")
	assert.NotEqual(t, resp.Error, resp.Code, "le champ 'error' doit etre le message humain, pas le code machine")
}

// === Lifecycle handlers (CONTRAT-HUB sec. 5.7-5.8) ===

// --- handleSoftDelete ---

func TestVeridianHandleSoftDelete_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	now := time.Now().UTC()
	svc.EXPECT().SoftDelete(gomock.Any(), domain.SoftDeleteInput{TenantID: "ws-1", Reason: "GDPR"}).
		Return(&domain.SoftDeleteResponse{
			TenantID:        "ws-1",
			Status:          "deleted",
			DeletedAt:       now,
			PurgeEligibleAt: now.Add(30 * 24 * time.Hour),
		}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleSoftDelete, http.MethodPost, "/api/tenants/ws-1/soft-delete", "ws-1", `{"reason":"GDPR"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.SoftDeleteResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.TenantID)
	assert.Equal(t, "deleted", resp.Status)
}

func TestVeridianHandleSoftDelete_NoBodyAllowed(t *testing.T) {
	// Body optionnel : pas de body = soft-delete sans reason (back-compat).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SoftDelete(gomock.Any(), domain.SoftDeleteInput{TenantID: "ws-1"}).
		Return(&domain.SoftDeleteResponse{TenantID: "ws-1", Status: "deleted", DeletedAt: time.Now(), PurgeEligibleAt: time.Now().Add(30 * 24 * time.Hour)}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleSoftDelete, http.MethodPost, "/api/tenants/ws-1/soft-delete", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandleSoftDelete_TenantNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SoftDelete(gomock.Any(), gomock.Any()).Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleSoftDelete, http.MethodPost, "/api/tenants/ghost/soft-delete", "ghost", `{}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, ErrCodeTenantNotFound, decodeErrCode(t, rec.Body.Bytes()))
}

// --- handleRestore ---

func TestVeridianHandleRestore_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Restore(gomock.Any(), domain.RestoreInput{TenantID: "ws-1", Reason: "ticket #42"}).
		Return(&domain.RestoreResponse{
			TenantID:   "ws-1",
			Status:     "active",
			RestoredAt: time.Now().UTC(),
		}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleRestore, http.MethodPost, "/api/tenants/ws-1/restore", "ws-1", `{"reason":"ticket #42"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.RestoreResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "active", resp.Status)
}

func TestVeridianHandleRestore_NotSoftDeleted_409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Restore(gomock.Any(), gomock.Any()).Return(nil, service.ErrTenantNotSoftDeleted)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleRestore, http.MethodPost, "/api/tenants/ws-1/restore", "ws-1", `{}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ErrCodeTenantSoftDeleted, decodeErrCode(t, rec.Body.Bytes()))
}

// --- handlePurge ---

func TestVeridianHandlePurge_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Purge(gomock.Any(), domain.PurgeInput{
		TenantID: "ws-1", Reason: "GDPR final", Confirm: "PURGE",
	}).Return(&domain.PurgeResponse{
		TenantID: "ws-1",
		Status:   "purged",
		PurgedAt: time.Now().UTC(),
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handlePurge, http.MethodPost, "/api/tenants/ws-1/purge", "ws-1",
		`{"reason":"GDPR final","confirm":"PURGE"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianHandlePurge_RequiresBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handlePurge, http.MethodPost, "/api/tenants/ws-1/purge", "ws-1", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianHandlePurge_RejectsMissingFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handlePurge, http.MethodPost, "/api/tenants/ws-1/purge", "ws-1", `{"reason":""}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, ErrCodeInvalidPayload, decodeErrCode(t, rec.Body.Bytes()))
}

func TestVeridianHandlePurge_NotEligible_409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Purge(gomock.Any(), gomock.Any()).Return(nil, service.ErrPurgeNotEligible)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handlePurge, http.MethodPost, "/api/tenants/ws-1/purge", "ws-1",
		`{"reason":"x","confirm":"PURGE"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, ErrCodePurgeNotEligible, decodeErrCode(t, rec.Body.Bytes()))
}

// --- handleTouch ---

func TestVeridianHandleTouch_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Touch(gomock.Any(), "ws-1").Return(&domain.TouchResponse{
		TenantID:  "ws-1",
		TouchedAt: time.Now().UTC(),
		Debounced: false,
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTouch, http.MethodPost, "/api/tenants/ws-1/touch", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.TouchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Debounced)
}

func TestVeridianHandleTouch_Debounced(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Touch(gomock.Any(), "ws-1").Return(&domain.TouchResponse{
		TenantID:  "ws-1",
		TouchedAt: time.Now().UTC().Add(-1 * time.Hour),
		Debounced: true,
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleTouch, http.MethodPost, "/api/tenants/ws-1/touch", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code, "debounce reste un 200 OK (no-op silencieux)")
	var resp domain.TouchResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Debounced)
}

// --- handleUsageSummary ---

func TestVeridianHandleUsageSummary_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UsageSummary(gomock.Any(), "ws-1").Return(&domain.UsageSummaryResponse{
		TenantID:        "ws-1",
		MessagesSent30d: 1234,
		Plan:            "pro",
		Status:          domain.PlanStatusActive,
		ContactsCount:   0,
		GeneratedAt:     time.Now().UTC(),
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleUsageSummary, http.MethodGet, "/api/tenants/ws-1/usage-summary", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.UsageSummaryResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, int64(1234), resp.MessagesSent30d)
}

func TestVeridianHandleUsageSummary_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UsageSummary(gomock.Any(), "ghost").Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleUsageSummary, http.MethodGet, "/api/tenants/ghost/usage-summary", "ghost", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, ErrCodeTenantNotFound, decodeErrCode(t, rec.Body.Bytes()))
}

// --- Routes registration ---

// TestVeridianHandler_SetIdempotencyRepo verifie le setter d'injection
// du repo idempotency (CONTRAT-HUB sec. 5.11). Pattern utilise par app.go
// pour eviter une signature constructor surchargee. Quand le repo n'est
// pas set, le middleware idempotency reste passthrough (cf. tests
// middleware/veridian_idempotency_test.go).
func TestVeridianHandler_SetIdempotencyRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	h := NewVeridianHandler(svc, logger.NewLogger())
	assert.Nil(t, h.idempotencyRepo, "default nil — middleware passthrough en mode self-hosted")

	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)
	h.SetIdempotencyRepo(repo)
	assert.NotNil(t, h.idempotencyRepo, "set apres injection app.go")
}

func TestVeridianHandler_LifecycleRoutes_Registered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret")

	for _, path := range []string{
		"/api/tenants/ws-1/soft-delete",
		"/api/tenants/ws-1/restore",
		"/api/tenants/ws-1/purge",
		"/api/tenants/ws-1/touch",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		_, pattern := mux.Handler(req)
		assert.NotEmpty(t, pattern, "POST %s should be registered", path)
	}
	// usage-summary est GET
	req := httptest.NewRequest(http.MethodGet, "/api/tenants/ws-1/usage-summary", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "GET /api/tenants/:id/usage-summary should be registered")
}

// TestVeridianHandler_GrantUnlimitedRoute_Registered verifie que la route
// POST /api/veridian/admin/grant-unlimited est bien enregistree via
// RegisterRoutes. Si quelqu'un supprime accidentellement la ligne du mux.Handle,
// ce test fail et evite une regression silencieuse de l'echappatoire interne
// equipe + clients fideles.
func TestVeridianHandler_GrantUnlimitedRoute_Registered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/grant-unlimited", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/veridian/admin/grant-unlimited should be registered")
	assert.Contains(t, pattern, "grant-unlimited", "route should target grant-unlimited handler")
}

// === handleLimits (V37 pricing-plans lot 7) ===
// Endpoint GET /api/tenants/{id}/limits, auth HMAC, renvoie LimitsResponse.

func TestVeridianHandleLimits_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	now := time.Now().UTC()
	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GetLimits(gomock.Any(), "ws-pro").Return(&domain.LimitsResponse{
		TenantID:   "ws-pro",
		Plan:       "pro",
		PlanSource: domain.PlanSourceStripe,
		Status:     domain.PlanStatusActive,
		Limits: domain.PlanLimits{
			MonthlyEmailQuota:      -1,
			MaxContacts:            5000,
			MaxSeats:               5,
			MaxOAuthAccounts:       5,
			MaxCustomDomains:       1,
			MaxActiveSequences:     -1,
			FeatureABTesting:       true,
			FeatureBrandingRemoved: true,
			FeatureWhiteLabel:      false,
			HistoryRetentionDays:   365,
		},
		GeneratedAt: now,
	}, nil)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleLimits, http.MethodGet, "/api/tenants/ws-pro/limits", "ws-pro", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp domain.LimitsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-pro", resp.TenantID)
	assert.Equal(t, "pro", resp.Plan)
	assert.Equal(t, domain.PlanSourceStripe, resp.PlanSource)
	assert.Equal(t, int64(5000), resp.Limits.MaxContacts)
	assert.Equal(t, 5, resp.Limits.MaxSeats)
	assert.True(t, resp.Limits.FeatureABTesting)
	assert.False(t, resp.Limits.FeatureWhiteLabel, "Pro != Business white-label")
}

func TestVeridianHandleLimits_MissingID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleLimits, http.MethodGet, "/api/tenants//limits", "", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleLimits_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GetLimits(gomock.Any(), "ws-ghost").Return(nil, sql.ErrNoRows)
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleLimits, http.MethodGet, "/api/tenants/ws-ghost/limits", "ws-ghost", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestVeridianHandleLimits_InternalError(t *testing.T) {
	// Erreur autre que sql.ErrNoRows → 500 (ne pas leaker le detail au caller).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().GetLimits(gomock.Any(), "ws-broken").Return(nil, errors.New("planRepo: connection refused"))
	h := newHandlerWithService(svc)

	rec := postWithPathValue(t, h.handleLimits, http.MethodGet, "/api/tenants/ws-broken/limits", "ws-broken", "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestVeridianHandleLimits_RegisteredInRoutes — la route doit etre cablee
// dans RegisterRoutes. Garde-fou contre suppression accidentelle.
func TestVeridianHandleLimits_RegisteredInRoutes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodGet, "/api/tenants/ws-1/limits", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "GET /api/tenants/:id/limits should be registered")
	assert.Contains(t, pattern, "limits", "route should target limits handler")
}

// TestVeridianRouteRegistered_DiscoveryByEmail valide que la route
// POST /api/users/by-email est bien enregistree dans le mux veridian
// (regression guard — Constitution §1 routes API coverage 100%).
func TestVeridianRouteRegistered_DiscoveryByEmail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/users/by-email", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/users/by-email should be registered")
	assert.Contains(t, pattern, "by-email", "route should target discovery handler")
}

// === Lot G — handleListTenants (2026-05-21) ===

func TestVeridianHandleListTenants_OK_Managed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().ListTenants(gomock.Any(), gomock.Any()).
		Return(&domain.ListTenantsResponse{
			Managed: []domain.TenantSummary{
				{TenantID: "test1", HasPlan: true, Plan: "free", Status: "active"},
			},
			Total: 1,
		}, nil)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/tenants?prefix=test", nil)
	rec := httptest.NewRecorder()
	h.handleListTenants(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.ListTenantsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Managed, 1)
}

func TestVeridianHandleListTenants_IncludeOrphans_QueryParam(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	// Capture l'input pour valider le parsing query params.
	var captured domain.ListTenantsInput
	svc.EXPECT().ListTenants(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ interface{}, input domain.ListTenantsInput) (*domain.ListTenantsResponse, error) {
			captured = input
			return &domain.ListTenantsResponse{
				Managed: []domain.TenantSummary{},
				Orphans: []domain.TenantSummary{{TenantID: "ghost1", HasPlan: false}},
				Total:   1,
			}, nil
		})
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/veridian/admin/tenants?prefix=test&include_orphans=true&limit=10", nil)
	rec := httptest.NewRecorder()
	h.handleListTenants(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test", captured.Prefix)
	assert.True(t, captured.IncludeOrphans)
	assert.Equal(t, 10, captured.Limit)
}

func TestVeridianHandleListTenants_InvalidLimit_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/veridian/admin/tenants?limit=notanumber", nil)
	rec := httptest.NewRecorder()
	h.handleListTenants(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid limit")
}

func TestVeridianHandleListTenants_NegativeLimit_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/veridian/admin/tenants?limit=-5", nil)
	rec := httptest.NewRecorder()
	h.handleListTenants(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianHandleListTenants_PrefixValidationError_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().ListTenants(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("prefix must be at least 3 chars (got \"ab\")"))
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/veridian/admin/tenants?prefix=ab", nil)
	rec := httptest.NewRecorder()
	h.handleListTenants(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code,
		"validation errors with 'prefix' in message → 400 (pas 500)")
	assert.Contains(t, rec.Body.String(), "at least 3 chars")
}

func TestVeridianHandleListTenants_ServiceError_500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().ListTenants(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))
	h := newHandlerWithService(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/veridian/admin/tenants?prefix=test", nil)
	rec := httptest.NewRecorder()
	h.handleListTenants(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "db down")
}

func TestVeridianHandleListTenants_RouteRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/tenants?prefix=test", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "GET /api/veridian/admin/tenants should be registered")
	assert.Contains(t, pattern, "tenants")
}

// === Veridian patch — Lot K (2026-05-21) ===
// Validations route-level pour rotate-api-key + transfer-owner.
// Les unit tests des handlers sont dans veridian_rotate_transfer_handler_test.go ;
// ici on valide juste que les routes sont enregistrees dans le mux (pour
// satisfaire le check Nuclear routes API + le mapping 1-pour-1).

func TestVeridianHandleRotateAPIKey_RouteRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-1/rotate-api-key", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/tenants/{id}/rotate-api-key should be registered")
	assert.Contains(t, pattern, "rotate-api-key")
}

func TestVeridianHandleTransferOwner_RouteRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-1/transfer-owner", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/tenants/{id}/transfer-owner should be registered")
	assert.Contains(t, pattern, "transfer-owner")
}

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
// Anti-regression : les 3 routes /api/tenants/{id}/{sync,remove,restore}-member
// doivent rester enregistrees dans le mux Veridian. Si quelqu'un supprime
// mux.Handle, ces tests cassent en CI plutot que d'attendre l'E2E.
// Tests handler detailles dans veridian_membership_handler_test.go.

func TestVeridianHandleSyncMember_RouteRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-1/sync-member", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/tenants/{id}/sync-member should be registered")
	assert.Contains(t, pattern, "sync-member")
}

func TestVeridianHandleRemoveMember_RouteRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-1/remove-member", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/tenants/{id}/remove-member should be registered")
	assert.Contains(t, pattern, "remove-member")
}

func TestVeridianHandleRestoreMember_RouteRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-1/restore-member", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/tenants/{id}/restore-member should be registered")
	assert.Contains(t, pattern, "restore-member")
}

// === Veridian patch — lot O (2026-05-21) ===
// Anti-regression : la route GET /api/veridian/admin/pricing-cache doit
// rester enregistree dans le mux Veridian. Si quelqu'un supprime le
// mux.Handle, ce test casse en CI (vs detection tardive en e2e).
// Le test detail du handler est dans veridian_pricing_cache_handler_test.go.
func TestVeridianHandlePricingCache_RouteRegisteredInMainHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodGet, "/api/veridian/admin/pricing-cache", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "GET /api/veridian/admin/pricing-cache should be registered")
	assert.Contains(t, pattern, "pricing-cache")
}

// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8.3) ===
// Anti-regression : la route POST /api/sso/issue-magic-link doit rester
// enregistree dans le mux Veridian. Si quelqu'un supprime le mux.Handle,
// ce test casse en CI (vs detection tardive en e2e bounce OAuth).
// Le test detail du handler est dans veridian_sso_handler_test.go.
func TestVeridianHandleIssueMagicLink_RouteRegisteredInMainHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := newHandlerWithService(svc)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	req := httptest.NewRequest(http.MethodPost, "/api/sso/issue-magic-link", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/sso/issue-magic-link should be registered")
	assert.Contains(t, pattern, "issue-magic-link")
}

// === Veridian patch — sync v1.5 CONTRAT-HUB §5.22.2 (2026-05-23) ===
// Test que la route alias workspace-level prescrite par le contrat v1.4
// (`POST /api/veridian/workspaces/{tenantId}/attach-member`) est bien
// enregistree dans le mux et resout vers le handler `handleAttachMember`
// (mono-workspace : alias delegue au meme handler que la route tenant-level
// historique).
//
// Cf todo/2026-05-21-contrat-hub-v15-sync.md §2.1.
func TestVeridianRegisterRoutes_AttachMemberWorkspaceAlias(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	h := NewVeridianHandler(svc, logger.NewLogger())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret-hub-secret-32chars-min-ok-padding")

	// Alias workspace-level prescrit par §5.22.2.
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/workspaces/ws-1/attach-member", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/veridian/workspaces/{tenantId}/attach-member should be registered")
	assert.Contains(t, pattern, "attach-member")
	assert.Contains(t, pattern, "veridian/workspaces", "should match the workspace-level pattern, not the tenant-level one")

	// Route tenant-level historique toujours active.
	tenantReq := httptest.NewRequest(http.MethodPost, "/api/tenants/ws-1/attach-member", nil)
	_, tenantPattern := mux.Handler(tenantReq)
	assert.NotEmpty(t, tenantPattern, "POST /api/tenants/{tenantId}/attach-member should remain registered")
	assert.Contains(t, tenantPattern, "tenants", "tenant-level route must coexist with workspace-level alias")
}

// === AUDIT-TRIAL-RESIDUS-2026-05-24 — anti-régression invalidation cache ===
//
// Contexte : le ticket Hub `2026-05-23-audit-trial-residus-apres-paiement.md`
// a livré 2 fixes côté Hub pour garantir qu'un client qui paie ne voit plus
// aucun résidu trial. Côté Notifuse, le gap correspondant était que les
// handlers UpdatePlan / Resume / Restore / Suspend / SoftDelete ne purgeaient
// PAS le PaywallCache après succès → fenêtre ≤60s pendant laquelle :
//   - middleware paywall sert l'ancien plan/status
//   - middleware soft-deleted continue à obfusquer un tenant pourtant restored
//   - UI affiche encore le bandeau "Free — 15-day trial" alors que plan=pro en DB
//
// Ces tests vérifient que chaque handler purge le cache pour le tenantID
// concerné après une mutation réussie. Pattern : seed via `cache.SeedForTest`,
// call handler, assert `cache.Has(...)` == false.
//
// Pattern de référence : handleGrantUnlimited (cf. veridian_grant_unlimited_handler.go §92).

func TestVeridianHandleUpdatePlan_InvalidatesCacheOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-pro")
	require.True(t, cache.Has("ws-pro"), "seed sentinel doit être présent avant la call")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).
		Return(&domain.UpdatePlanResponse{
			TenantID:     "ws-pro",
			Plan:         "pro",
			PreviousPlan: "free",
			PlanSource:   domain.PlanSourceStripe,
			AppliedAt:    time.Now().UTC(),
		}, nil)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-pro","plan":"pro","plan_source":"stripe"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, cache.Has("ws-pro"),
		"cache doit être invalidé après UpdatePlan success — sinon paywall sert plan stale 60s")
}

func TestVeridianHandleUpdatePlan_DoesNotInvalidateOnError(t *testing.T) {
	// Garde-fou : sur erreur service (PlanImmune, NotFound, etc.), le cache
	// NE doit PAS être purgé inutilement (sinon coût DB lookup gratuit).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-vip")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).
		Return(nil, service.ErrPlanImmune)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-vip","plan":"free","plan_source":"stripe"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.True(t, cache.Has("ws-vip"),
		"cache doit rester intact sur erreur service (rien n'a changé en DB)")
}

func TestVeridianHandleSuspend_InvalidatesCacheOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-1")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Suspend(gomock.Any(), gomock.Any()).Return(nil)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postJSON(t, h.handleSuspend, "/api/tenants/suspend",
		`{"tenant_id":"ws-1","reason":"manual"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, cache.Has("ws-1"),
		"cache doit être invalidé après Suspend — un envoi qui passait avant doit être bloqué immédiatement")
}

func TestVeridianHandleResume_InvalidatesCacheOnSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-1")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Resume(gomock.Any(), gomock.Any()).Return(nil)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postJSON(t, h.handleResume, "/api/tenants/resume",
		`{"tenant_id":"ws-1"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, cache.Has("ws-1"),
		"cache doit être invalidé après Resume — les writes doivent être débloqués immédiatement")
}

func TestVeridianHandleDelete_InvalidatesCacheOnSuccess(t *testing.T) {
	// Route legacy DELETE /api/tenants/{id} — délègue à SoftDelete service.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-deleteme")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SoftDelete(gomock.Any(), gomock.Any()).
		Return(&domain.SoftDeleteResponse{
			TenantID:        "ws-deleteme",
			Status:          "deleted",
			DeletedAt:       time.Now().UTC(),
			PurgeEligibleAt: time.Now().UTC().Add(30 * 24 * time.Hour),
		}, nil)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postWithPathValue(t, h.handleDelete, http.MethodDelete, "/api/tenants/ws-deleteme", "ws-deleteme", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, cache.Has("ws-deleteme"),
		"cache doit être invalidé après SoftDelete (route legacy) — les middlewares doivent voir deleted_at immédiatement")
}

func TestVeridianHandleSoftDelete_InvalidatesCacheOnSuccess(t *testing.T) {
	// Route nouvelle POST /api/tenants/{id}/soft-delete.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-sd")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().SoftDelete(gomock.Any(), gomock.Any()).
		Return(&domain.SoftDeleteResponse{
			TenantID:        "ws-sd",
			Status:          "deleted",
			DeletedAt:       time.Now().UTC(),
			PurgeEligibleAt: time.Now().UTC().Add(30 * 24 * time.Hour),
		}, nil)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postWithPathValue(t, h.handleSoftDelete, http.MethodPost,
		"/api/tenants/ws-sd/soft-delete", "ws-sd", `{"reason":"trial_expired"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, cache.Has("ws-sd"),
		"cache doit être invalidé après SoftDelete (route v1.4) — middleware doit bloquer writes immédiatement")
}

func TestVeridianHandleRestore_InvalidatesCacheOnSuccess(t *testing.T) {
	// === Cas critique audit trial résidus §C : Restore inverse soft-delete ===
	// Sans invalidation, le middleware soft-deleted continue à obfusquer le
	// tenant restored pendant ≤60s — exactement le résidu que le ticket Hub
	// nous demande d'éliminer.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-restored")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Restore(gomock.Any(), gomock.Any()).
		Return(&domain.RestoreResponse{
			TenantID:   "ws-restored",
			Status:     string(domain.PlanStatusActive),
			RestoredAt: time.Now().UTC(),
		}, nil)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postWithPathValue(t, h.handleRestore, http.MethodPost,
		"/api/tenants/ws-restored/restore", "ws-restored", `{"reason":"payment_received"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, cache.Has("ws-restored"),
		"cache doit être invalidé après Restore — sinon middleware soft-deleted continue obfusquer 60s")
}

func TestVeridianHandleRestore_DoesNotInvalidateOnError(t *testing.T) {
	// Garde-fou : ErrTenantNotSoftDeleted (409) ne doit pas purger le cache.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cache := middleware.NewPaywallCache()
	cache.SeedForTest("ws-active")

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().Restore(gomock.Any(), gomock.Any()).
		Return(nil, service.ErrTenantNotSoftDeleted)
	h := newHandlerWithServiceAndCache(svc, cache)

	rec := postWithPathValue(t, h.handleRestore, http.MethodPost,
		"/api/tenants/ws-active/restore", "ws-active", "")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.True(t, cache.Has("ws-active"),
		"cache doit rester intact sur erreur (rien n'a changé en DB)")
}

// TestVeridianHandle_CacheInvalidationGracefulWithoutCache vérifie qu'aucun
// handler ne panic si paywallCache n'est PAS injecté (mode self-hosted sans
// middleware paywall). Tous les call sites font `if h.paywallCache != nil`.
func TestVeridianHandle_CacheInvalidationGracefulWithoutCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().UpdatePlan(gomock.Any(), gomock.Any()).
		Return(&domain.UpdatePlanResponse{
			TenantID: "ws-1", Plan: "pro",
			PlanSource: domain.PlanSourceStripe,
			AppliedAt:  time.Now().UTC(),
		}, nil)
	svc.EXPECT().Resume(gomock.Any(), gomock.Any()).Return(nil)
	svc.EXPECT().Restore(gomock.Any(), gomock.Any()).
		Return(&domain.RestoreResponse{TenantID: "ws-1", Status: "active", RestoredAt: time.Now().UTC()}, nil)

	h := newHandlerWithService(svc) // cache nil

	// Aucun de ces 3 appels ne doit panic même sans cache injecté.
	rec1 := postJSON(t, h.handleUpdatePlan, "/api/tenants/update-plan",
		`{"tenant_id":"ws-1","plan":"pro","plan_source":"stripe"}`)
	assert.Equal(t, http.StatusOK, rec1.Code)

	rec2 := postJSON(t, h.handleResume, "/api/tenants/resume", `{"tenant_id":"ws-1"}`)
	assert.Equal(t, http.StatusOK, rec2.Code)

	rec3 := postWithPathValue(t, h.handleRestore, http.MethodPost,
		"/api/tenants/ws-1/restore", "ws-1", "")
	assert.Equal(t, http.StatusOK, rec3.Code)
}
// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21, 2026-05-25) ===

// TestVeridianHandler_SetFrozenCache : le setter optionnel doit accepter nil
// (mode self-hosted sans freeze) ET un cache non-nil (mode prod). Pas de panic.
func TestVeridianHandler_SetFrozenCache(t *testing.T) {
	h := &VeridianHandler{logger: logger.NewLogger()}
	assert.Nil(t, h.frozenCache, "cache nil par defaut (mode self-hosted)")

	cache := middleware.NewFrozenMemberCache()
	h.SetFrozenCache(cache)
	assert.NotNil(t, h.frozenCache)

	// Setter idempotent : reset a nil OK (utile pour les tests).
	h.SetFrozenCache(nil)
	assert.Nil(t, h.frozenCache)
}

// TestVeridianHandler_RegisterRoutes_FreezeUnfreeze : verifie que les 2 routes
// freeze/unfreeze sont bien enregistrees sur le mux apres RegisterRoutes.
// Lance les routes via le mux pour s'assurer qu'elles match le pattern
// /api/tenants/{tenantId}/(un)freeze-member sans erreur "no route".
func TestVeridianHandler_RegisterRoutes_FreezeUnfreeze(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	svc.EXPECT().FreezeMember(gomock.Any(), gomock.Any()).Return(&domain.FreezeMemberResponse{
		TenantID: "ws-1", UserEmail: "bob@x.test", Reason: domain.FreezeReasonManual,
	}, false, nil).AnyTimes()
	svc.EXPECT().UnfreezeMember(gomock.Any(), gomock.Any()).Return(&domain.UnfreezeMemberResponse{
		TenantID: "ws-1", UserEmail: "bob@x.test",
	}, true, nil).AnyTimes()

	h := newHandlerWithService(svc)
	mux := http.NewServeMux()
	// HUB_API_SECRET vide → middleware HMAC laisse passer 503 mais routes enregistrees.
	// On utilise un secret arbitraire pour que le HMAC se calcule normalement
	// — mais on ne signe pas, donc on attend 401. Ce qui importe est que la
	// route MATCH (pas 404 method-not-allowed / no-route).
	h.RegisterRoutes(mux, "test-secret-for-route-check-padding-ok")

	for _, path := range []string{
		"/api/tenants/ws-1/freeze-member",
		"/api/tenants/ws-1/unfreeze-member",
	} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// 401 = route matched + HMAC middleware reject. 404 = route NOT matched.
		assert.NotEqual(t, http.StatusNotFound, rec.Code, "route %s must be registered", path)
		assert.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, "route %s must accept POST", path)
	}
}
