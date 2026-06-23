package http

// === Veridian patch ===
// Handler pour POST /api/veridian/admin/grant-unlimited.
//
// Securité : HMAC-protected (meme middleware que les autres endpoints
// /api/veridian/admin/*). Le caller doit signer la requete avec
// HUB_API_SECRET — ce qui restreint l'usage a Robert (qui detient le secret)
// ou a une automation Hub autorisee.
//
// Use case : passer un workspace en illimite pour
//   - membre interne equipe Veridian (toi-meme, futurs employes)
//   - client fidele / partenaire (lifetime offer)
//   - compensation post-incident (downtime / data loss)
//
// Le tenant devient `plan=enterprise + quota=-1 + plan_source=lifetime_partner`
// donc immune au downgrade Stripe automatique. Invalidation du cache paywall
// en post-traitement pour effet immediat (pas d'attente TTL 60s).

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

// handleGrantUnlimited expose POST /api/veridian/admin/grant-unlimited.
//
// Body :
//   {
//     "tenant_id": "robertbrunon",
//     "reason": "internal_team_member",
//     "plan_source": "lifetime_partner"  // optionnel, defaut lifetime_partner
//   }
//
// Reponse 200 :
//   {
//     "tenant_id": "robertbrunon",
//     "plan": "enterprise",
//     "previous_plan": "free",
//     "plan_source": "lifetime_partner",
//     "quota": -1,
//     "granted_at": "2026-05-20T13:00:00Z",
//     "reason": "internal_team_member"
//   }
//
// Erreurs :
//   - 400 invalid_payload : tenant_id ou reason manquant, JSON invalide
//   - 400 invalid_payload : plan_source fourni non immune (ex: stripe)
//   - 404 tenant_not_found : workspace_id inconnu
//   - 500 internal_error : erreur DB
func (h *VeridianHandler) handleGrantUnlimited(w http.ResponseWriter, r *http.Request) {
	var input domain.GrantUnlimitedInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" || input.Reason == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and reason are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.Reason == "", "reason"),
		})
		return
	}

	resp, err := h.service.GrantUnlimited(r.Context(), input)
	if err != nil {
		h.logError("grant_unlimited", err, map[string]interface{}{
			"tenant_id": input.TenantID,
			"reason":    input.Reason,
		})
		if errors.Is(err, service.ErrInvalidPlanSourceForGrant) || errors.Is(err, service.ErrGrantReasonRequired) {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, err.Error(), http.StatusBadRequest, map[string]interface{}{
				"hint": "plan_source must be one of: lifetime_partner, lifetime_site_vitrine, manual, internal (or empty for default)",
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

	// Invalidate cache paywall pour effet immediat (un envoi qui etait bloque
	// par le quota va etre debloque instantanement, sans attendre 60s TTL).
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(input.TenantID)
	}

	writeJSON(w, http.StatusOK, resp)
}
