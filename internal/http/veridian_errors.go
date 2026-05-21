package http

import (
	"encoding/json"
	"net/http"
)

// Codes d'erreur machine-readable exposes par les endpoints Veridian.
//
// Source de verite : CONTRAT-HUB.md sec. 5.10. La liste est figee : tout nouveau
// code doit etre ajoute ici ET dans le contrat Hub avant utilisation.
const (
	ErrCodeInvalidPayload         = "invalid_payload"
	ErrCodeUnauthorized           = "unauthorized"
	ErrCodeForbidden              = "forbidden"
	ErrCodeMethodNotAllowed       = "method_not_allowed"
	ErrCodeTenantNotFound         = "tenant_not_found"
	ErrCodeTenantSoftDeleted      = "tenant_soft_deleted"
	ErrCodeOwnerMismatch          = "owner_mismatch"
	ErrCodeQuotaExceeded          = "quota_exceeded"
	ErrCodePlanNotFound           = "plan_not_found"
	ErrCodePlanLocked             = "plan_locked"
	ErrCodeUserNotFound           = "user_not_found"
	ErrCodeApiKeyMultiWorkspace   = "api_key_multi_workspace"
	ErrCodeApiKeyNoWorkspace      = "api_key_no_workspace"
	ErrCodeIdempotencyKeyMismatch = "idempotency_key_mismatch"
	ErrCodePurgeNotEligible       = "purge_not_eligible"
	ErrCodePaywallUnavailable     = "paywall_unavailable"
	ErrCodeInternalError          = "internal_error"
	// === attach-member (2026-05-21) ===
	ErrCodeTenantSuspended        = "tenant_suspended"
	ErrCodeInvalidRole            = "invalid_role"
	ErrCodeUserRoleConflict       = "user_role_conflict"
	// === résilience billing Hub (V39, 2026-05-21) ===
	// ErrCodeHubSyncDead est retourné par le middleware paywall quand
	// last_hub_sync_at > 72h (Hub considéré mort). Distinct de ErrCodePaywallUnavailable
	// (erreur DB) et de ErrCodeTenantSoftDeleted (décision business Hub) :
	// ici c'est un incident infra Hub (pas une décision business).
	// Le Hub peut lever cette dégradation en envoyant n'importe quelle mutation
	// (Touch, UpdatePlan, etc.) qui appellera TouchHubSync et rafraîchira le timestamp.
	ErrCodeHubSyncDead            = "hub_sync_dead"
)

// VeridianErrorResponse est le format d'erreur additif des endpoints Veridian.
//
// Champ `error` (message humain) est conserve pour retro-compat client Hub
// (cf. veridian-hub/lib/notifuse/client.ts:243 qui lit `errorBody.error` comme
// message lisible). Le champ `code` (machine-readable) est ajoute en parallele
// pour permettre aux clients de brancher du switch logique sans parser la
// chaine humaine. Une fois le Hub aligne (lecture de `code`), on pourra
// basculer `error` en code machine et `message` en humain, conformement strict
// au sec. 5.10. Pour l'instant, additif.
type VeridianErrorResponse struct {
	Error   string                 `json:"error"`
	Code    string                 `json:"code"`
	Message string                 `json:"message,omitempty"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// missingFields construit une liste des champs manquants pour les details
// d'erreur invalid_payload. Appel : missingFields(cond1, "field1", cond2, "field2", ...).
// Pratique pour les handlers qui valident plusieurs champs requis.
func missingFields(conds ...interface{}) []string {
	out := make([]string, 0, len(conds)/2)
	for i := 0; i+1 < len(conds); i += 2 {
		missing, okBool := conds[i].(bool)
		name, okStr := conds[i+1].(string)
		if okBool && okStr && missing {
			out = append(out, name)
		}
	}
	return out
}

// WriteJSONErrorCode emet une reponse d'erreur enrichie avec un code machine.
//
// Le champ `error` est duplique avec `message` pour retro-compat (cf.
// VeridianErrorResponse). `details` est facultatif : passer un nil ou map vide
// est equivalent.
func WriteJSONErrorCode(w http.ResponseWriter, code, message string, statusCode int, details map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(VeridianErrorResponse{
		Error:   message,
		Code:    code,
		Message: message,
		Details: details,
	})
}
