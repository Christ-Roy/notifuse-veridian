package http

// === Veridian patch ===
// VeridianHandler expose les endpoints /api/tenants/* appeles par le Hub
// Veridian. Toutes les routes sont protegees par middleware HMAC
// (X-Veridian-Hub-Signature). Voir veridian_hmac.go.
//
// Routes :
//   POST   /api/tenants/provision      -> ProvisionResponse
//   POST   /api/tenants/update-plan    -> {tenant_id, plan, applied_at}
//   POST   /api/tenants/suspend        -> {tenant_id, suspended_at}
//   POST   /api/tenants/resume         -> {tenant_id, resumed_at}
//   DELETE /api/tenants/{id}           -> {tenant_id, deleted_at}
//   GET    /api/tenants/{id}/status    -> StatusResponse

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianHandler regroupe les handlers Hub-driven.
type VeridianHandler struct {
	service      domain.VeridianService
	logger       logger.Logger
	paywallCache *middleware.PaywallCache // Peut etre nil (mode self-hosted sans paywall)
}

// NewVeridianHandler cree un handler. Le paywallCache est optionnel : s'il
// est nil, l'endpoint /api/veridian/admin/cache/invalidate retournera 503.
func NewVeridianHandler(service domain.VeridianService, log logger.Logger) *VeridianHandler {
	return &VeridianHandler{
		service: service,
		logger:  log,
	}
}

// SetPaywallCache injecte un cache partage avec le middleware paywall pour
// que /api/veridian/admin/cache/invalidate puisse le purger sur demande Hub.
// Optionnel : si jamais set, l'endpoint retournera 503 Service Unavailable.
func (h *VeridianHandler) SetPaywallCache(cache *middleware.PaywallCache) {
	h.paywallCache = cache
}

// RegisterRoutes enregistre les 6 endpoints /api/tenants/* WRAPPES dans
// le middleware HMAC. hubSecret est la valeur de HUB_API_SECRET.
//
// Utilise les patterns Go 1.22+ "METHOD path" pour matcher la methode
// et extraire {id} via r.PathValue("id").
func (h *VeridianHandler) RegisterRoutes(mux *http.ServeMux, hubSecret string) {
	hmac := middleware.VeridianHMACMiddleware(hubSecret)

	mux.Handle("POST /api/tenants/provision", hmac(http.HandlerFunc(h.handleProvision)))
	mux.Handle("POST /api/tenants/update-plan", hmac(http.HandlerFunc(h.handleUpdatePlan)))
	mux.Handle("POST /api/tenants/suspend", hmac(http.HandlerFunc(h.handleSuspend)))
	mux.Handle("POST /api/tenants/resume", hmac(http.HandlerFunc(h.handleResume)))
	mux.Handle("DELETE /api/tenants/{id}", hmac(http.HandlerFunc(h.handleDelete)))
	mux.Handle("GET /api/tenants/{id}/status", hmac(http.HandlerFunc(h.handleStatus)))
	// === Veridian patch === Admin endpoint pour cleanup CI / tests.
	// Supprime DEFINITIVEMENT (hard delete) workspace + DB + plan row.
	// Refuse les tenants matchant safety_client_prefixes (clients reels).
	mux.Handle("POST /api/veridian/admin/wipe-test-tenants", hmac(http.HandlerFunc(h.handleWipeTestTenants)))
	// === Veridian patch === Cache invalidate pour eliminer le sleep 60s
	// des tests e2e paywall apres suspend/resume/update-plan/delete. En prod,
	// peut etre appele par le Hub pour propager rapidement un changement de
	// plan a Notifuse sans attendre l'expiration TTL.
	mux.Handle("POST /api/veridian/admin/cache/invalidate", hmac(http.HandlerFunc(h.handleInvalidateCache)))
}

func (h *VeridianHandler) handleProvision(w http.ResponseWriter, r *http.Request) {
	var input domain.ProvisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.TenantID == "" || input.OwnerEmail == "" {
		WriteJSONError(w, "tenant_id and owner_email are required", http.StatusBadRequest)
		return
	}

	resp, err := h.service.Provision(r.Context(), input)
	if err != nil {
		h.logError("provision", err, map[string]interface{}{
			"tenant_id": input.TenantID,
			"email":     input.OwnerEmail,
		})
		// === Veridian patch === Sentinel ErrTenantSoftDeleted → 409 Conflict
		// (re-provision rejetee tant que purge 30j pas passee).
		if errors.Is(err, service.ErrTenantSoftDeleted) {
			WriteJSONError(w, err.Error(), http.StatusConflict)
			return
		}
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	var input domain.UpdatePlanInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.TenantID == "" || input.Plan == "" {
		WriteJSONError(w, "tenant_id and plan are required", http.StatusBadRequest)
		return
	}

	if err := h.service.UpdatePlan(r.Context(), input); err != nil {
		h.logError("update_plan", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONError(w, "tenant not found", http.StatusNotFound)
			return
		}
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":  input.TenantID,
		"plan":       input.Plan,
		"applied_at": time.Now().UTC(),
	})
}

func (h *VeridianHandler) handleSuspend(w http.ResponseWriter, r *http.Request) {
	var input domain.SuspendInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.TenantID == "" {
		WriteJSONError(w, "tenant_id is required", http.StatusBadRequest)
		return
	}

	if err := h.service.Suspend(r.Context(), input); err != nil {
		h.logError("suspend", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONError(w, "tenant not found", http.StatusNotFound)
			return
		}
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":    input.TenantID,
		"suspended_at": time.Now().UTC(),
	})
}

func (h *VeridianHandler) handleResume(w http.ResponseWriter, r *http.Request) {
	var input domain.ResumeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.TenantID == "" {
		WriteJSONError(w, "tenant_id is required", http.StatusBadRequest)
		return
	}

	if err := h.service.Resume(r.Context(), input); err != nil {
		h.logError("resume", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONError(w, "tenant not found", http.StatusNotFound)
			return
		}
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":  input.TenantID,
		"resumed_at": time.Now().UTC(),
	})
}

func (h *VeridianHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONError(w, "tenant id is required", http.StatusBadRequest)
		return
	}

	if err := h.service.SoftDelete(r.Context(), tenantID); err != nil {
		h.logError("soft_delete", err, map[string]interface{}{"tenant_id": tenantID})
		if isNotFoundErr(err) {
			WriteJSONError(w, "tenant not found", http.StatusNotFound)
			return
		}
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":  tenantID,
		"deleted_at": time.Now().UTC(),
	})
}

func (h *VeridianHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONError(w, "tenant id is required", http.StatusBadRequest)
		return
	}

	resp, err := h.service.GetStatus(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONError(w, "tenant not found", http.StatusNotFound)
			return
		}
		h.logError("get_status", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// isNotFoundErr renvoie true sur sql.ErrNoRows ou erreurs wrappant ce sentinel.
// Les UPDATEs des repos retournent des erreurs construites avec fmt.Errorf
// "veridian_plan: workspace X not found" — on les reconnait par sous-chaine
// (pas ideal mais evite de creer un type d'erreur dedie pour le seul besoin
// du handler).
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	msg := err.Error()
	return msg != "" && (containsAny(msg, "not found", "ErrNoRows"))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf est une variante locale de strings.Index pour eviter d'importer
// strings juste pour ce helper.
func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func (h *VeridianHandler) logError(op string, err error, fields map[string]interface{}) {
	if h.logger == nil {
		return
	}
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["op"] = op
	fields["error"] = err.Error()
	h.logger.WithFields(fields).Error("veridian handler: operation failed")
}

// === Veridian patch ===
// handleWipeTestTenants supprime DEFINITIVEMENT les tenants matchant un prefix
// ou une liste explicite. Hard delete (DROP DATABASE upstream + DELETE plan row),
// pas un soft delete. Reserve aux tests CI / admin platform.
func (h *VeridianHandler) handleWipeTestTenants(w http.ResponseWriter, r *http.Request) {
	var input domain.WipeTestTenantsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.Prefix == "" && len(input.TenantIDs) == 0 {
		WriteJSONError(w, "prefix or tenant_ids required", http.StatusBadRequest)
		return
	}

	resp, err := h.service.WipeTestTenants(r.Context(), input)
	if err != nil {
		h.logError("wipe_test_tenants", err, map[string]interface{}{
			"prefix":     input.Prefix,
			"tenant_ids": len(input.TenantIDs),
		})
		WriteJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleInvalidateCache purge l'entree paywall cache pour un workspace_id
// donne. Idempotent : pas d'erreur si le workspace n'avait pas d'entree.
//
// Auth : middleware HMAC en amont (route enregistree dans hmac wrapper).
// Le timestamp drift de 5min + signature SHA256 reject les replays.
//
// Body :
//   {"workspace_id": "abc123"}
//
// Reponse :
//   {"workspace_id": "abc123", "invalidated": true}
//
// Si le PaywallCache n'a pas ete injecte (mode self-hosted sans paywall),
// on retourne 503 plutot qu'un 200 silencieusement faux : le caller (Hub)
// doit savoir que sa demande n'a pas eu d'effet.
func (h *VeridianHandler) handleInvalidateCache(w http.ResponseWriter, r *http.Request) {
	if h.paywallCache == nil {
		WriteJSONError(w, "paywall cache not initialized (self-hosted mode)", http.StatusServiceUnavailable)
		return
	}

	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if input.WorkspaceID == "" {
		WriteJSONError(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	h.paywallCache.Invalidate(input.WorkspaceID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workspace_id": input.WorkspaceID,
		"invalidated":  true,
	})
}

