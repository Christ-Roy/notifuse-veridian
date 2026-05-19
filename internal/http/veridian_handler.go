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

	"github.com/Notifuse/notifuse/internal/buildinfo"
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

	// === Veridian patch === Endpoint public (no HMAC) qui dit a la console UI
	// si on est en mode "Veridian-managed" (HUB_API_SECRET set) ou self-hosted.
	// En mode managed, la console doit cacher la page Create Workspace et
	// rediriger vers /console/signin (magic link). Voir console/src/pages/
	// CreateWorkspacePage.tsx.
	mux.HandleFunc("GET /api/veridian/mode", h.handleMode(hubSecret))

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
	// === Veridian patch === Repair endpoint pour les tenants existants dont
	// l'owner humain n'est pas attaché au workspace (workspaces créés avant
	// la feature Hub-Veridian). Idempotent : safe à appeler en boucle pour
	// réparer en batch. Voir todo/2026-05-17-provision-owner-attach.md.
	mux.Handle("POST /api/veridian/admin/attach-owner", hmac(http.HandlerFunc(h.handleAttachOwner)))
	// === Veridian patch === Health observable du tenant (livrable 3 contrat
	// intégrations Hub). Le Hub poll en cron 1×/h pour détecter régression
	// silencieuse du flow magic link Hub → app (bug 2026-05-17).
	mux.Handle("GET /api/tenants/{id}/health", hmac(http.HandlerFunc(h.handleHealth)))

	// === Veridian patch === Endpoint public (no HMAC) qui renvoie le tag
	// et le SHA git du binaire qui tourne. Permet à la CI de valider qu'un
	// redeploy a effectivement remplacé le container — défense contre le
	// faux positif "deploy success" sans changement réel d'image (bug
	// 2026-05-18 où compose.redeploy redéployait avec le même tag faute
	// de compose.update préalable). Utilisé par le step Verify prod runs
	// new code dans .github/workflows/veridian-ci.yml.
	mux.HandleFunc("GET /api/version", h.handleVersion)
}

func (h *VeridianHandler) handleProvision(w http.ResponseWriter, r *http.Request) {
	var input domain.ProvisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" || input.OwnerEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and owner_email are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.OwnerEmail == "", "owner_email"),
		})
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
			WriteJSONErrorCode(w, ErrCodeTenantSoftDeleted, err.Error(), http.StatusConflict, nil)
			return
		}
		// === Veridian patch === Sentinel ErrOwnerMismatch → 409 Conflict
		// (re-provision avec owner_email different refusee — protection contre
		// prise de controle d'un tenant existant). Contrat §5.1.
		if errors.Is(err, service.ErrOwnerMismatch) {
			WriteJSONErrorCode(w, ErrCodeOwnerMismatch, err.Error(), http.StatusConflict, nil)
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	var input domain.UpdatePlanInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" || input.Plan == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and plan are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.Plan == "", "plan"),
		})
		return
	}
	if !input.PlanSource.IsValid() {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid plan_source", http.StatusBadRequest, map[string]interface{}{
			"plan_source": string(input.PlanSource),
			"allowed":     []string{"", "stripe", "manual", "lifetime_site_vitrine", "lifetime_partner", "internal"},
		})
		return
	}

	resp, err := h.service.UpdatePlan(r.Context(), input)
	if err != nil {
		h.logError("update_plan", err, map[string]interface{}{"tenant_id": input.TenantID})
		// === Veridian patch === Sentinel ErrPlanImmune → 409 plan_locked
		// (downgrade Stripe webhook bloque sur un plan offert). Contrat sec. 3.3.
		if errors.Is(err, service.ErrPlanImmune) {
			WriteJSONErrorCode(w, ErrCodePlanLocked, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id":      input.TenantID,
				"requested_plan": input.Plan,
				"hint":           "plan_source is immune (lifetime_*/manual/internal); use a non-stripe plan_source to override",
			})
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleSuspend(w http.ResponseWriter, r *http.Request) {
	var input domain.SuspendInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	if err := h.service.Suspend(r.Context(), input); err != nil {
		h.logError("suspend", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
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
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	if err := h.service.Resume(r.Context(), input); err != nil {
		h.logError("resume", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
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
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	if err := h.service.SoftDelete(r.Context(), tenantID); err != nil {
		h.logError("soft_delete", err, map[string]interface{}{"tenant_id": tenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
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
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.GetStatus(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("get_status", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
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
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.Prefix == "" && len(input.TenantIDs) == 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "prefix or tenant_ids required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"prefix", "tenant_ids"},
		})
		return
	}

	resp, err := h.service.WipeTestTenants(r.Context(), input)
	if err != nil {
		h.logError("wipe_test_tenants", err, map[string]interface{}{
			"prefix":     input.Prefix,
			"tenant_ids": len(input.TenantIDs),
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
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
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "paywall cache not initialized (self-hosted mode)", http.StatusServiceUnavailable, nil)
		return
	}

	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.WorkspaceID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "workspace_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"workspace_id"},
		})
		return
	}

	h.paywallCache.Invalidate(input.WorkspaceID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workspace_id": input.WorkspaceID,
		"invalidated":  true,
	})
}


// handleAttachOwner répare un workspace existant en y attachant un user humain
// comme owner (cf. todo/2026-05-17-provision-owner-attach.md). Idempotent.
//
// Body :
//   {"tenant_id": "robertbrunon", "owner_email": "robert.brunon@veridian.site"}
//
// Réponse 200 :
//   {"tenant_id":"robertbrunon","owner_email":"robert.brunon@veridian.site",
//    "user_id":"0cb49456-...","attached":true,"already_attached":false,
//    "owner_transferred":true}
func (h *VeridianHandler) handleAttachOwner(w http.ResponseWriter, r *http.Request) {
	var input domain.AttachOwnerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" || input.OwnerEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and owner_email are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.OwnerEmail == "", "owner_email"),
		})
		return
	}

	resp, err := h.service.AttachOwner(r.Context(), input)
	if err != nil {
		h.logError("attach_owner", err, map[string]interface{}{
			"tenant_id":   input.TenantID,
			"owner_email": input.OwnerEmail,
		})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleHealth renvoie l'état réel observable du tenant (livrable 3 du
// contrat intégrations Hub). Auth HMAC. Renvoie 404 si tenant inexistant.
//
// Réponse 200 :
//
//	{"tenant_id":"...","workspace_id":"...","status":"active","owner_attached":true,
//	 "owner_email":"...","owner_user_id":"...","api_key_valid":true,
//	 "magic_link_capable":true,"members_count":2,"plan":"free","checked_at":"..."}
func (h *VeridianHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.Health(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("health", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleVersion renvoie le tag et le SHA git de l'image qui tourne (injectés
// au build via -ldflags -X). Endpoint public, pas de HMAC, idempotent.
// Réponse :
//
//	{"tag":"v32.0-veridian.eb7a88e2","git_sha":"eb7a88e2bc...","build_date":"2026-05-18T12:36:00Z"}
//
// Les 3 champs sont "dev" si le binaire est compilé sans ldflags
// (run local via `go run` ou `make dev`).
func (h *VeridianHandler) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"tag":        buildinfo.Tag,
		"git_sha":    buildinfo.GitSHA,
		"build_date": buildinfo.BuildDate,
	})
}

// handleMode renvoie le mode de deploiement Notifuse (managed vs self-hosted).
// Endpoint public (pas de HMAC) car consomme par la console UI pour decider si
// elle doit afficher la page Create Workspace ou rediriger vers /console/signin.
//
// Reponse :
//   { "mode": "veridian-managed" | "self-hosted",
//     "signin_url": "/console/signin",
//     "hub_url": "https://app.veridian.site" (uniquement si veridian-managed) }
//
// La presence de HUB_API_SECRET dans la config = mode veridian-managed.
func (h *VeridianHandler) handleMode(hubSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hubSecret != "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"mode":       "veridian-managed",
				"signin_url": "/console/signin",
				"hub_url":    "https://app.veridian.site",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"mode":       "self-hosted",
			"signin_url": "/console/signin",
		})
	}
}
