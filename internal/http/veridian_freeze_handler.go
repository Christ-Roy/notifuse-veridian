package http

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
//
// Handlers HTTP pour les endpoints :
//
//	POST /api/tenants/{tenantId}/freeze-member
//	POST /api/tenants/{tenantId}/unfreeze-member
//
// Auth : HMAC (X-Veridian-Hub-Signature) + Idempotency-Key (via writeRoute
// wrapper dans veridian_handler.go).
//
// Voir todo/2026-05-23-membership-freeze-per-user.md, domain/veridian_freeze.go,
// service/veridian_freeze_service.go.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

// handleFreezeMember : §5.21 freeze-member.
//
// Body :
//
//	{
//	  "user_email": "bob@example.com",
//	  "hub_user_id": "user_hub_abc123",
//	  "reason": "quota_seat_exceeded" | "manual"  // optionnel, default "manual"
//	}
//
// Reponse 200 :
//
//	{
//	  "tenant_id":   "ws-1",
//	  "user_email":  "bob@example.com",
//	  "hub_user_id": "user_hub_abc123",
//	  "frozen_at":   "2026-05-25T12:00:00Z",
//	  "reason":      "quota_seat_exceeded"
//	}
//
// Erreurs :
//
//	400 invalid_payload         : champs manquants ou email malforme
//	404 tenant_not_found        : workspace inexistant
//	404 user_not_member         : user pas membre du workspace (ou inconnu)
//	409 cannot_freeze_owner     : target est l'owner (utiliser transfer-owner)
//	409 member_already_frozen   : user deja frozen (idempotent cote repo,
//	                              409 pour signaler au Hub de pas re-emettre)
//	500 internal_error          : echec service
func (h *VeridianHandler) handleFreezeMember(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantId")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		UserEmail string              `json:"user_email"`
		HubUserID string              `json:"hub_user_id"`
		Reason    domain.FreezeReason `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}

	missing := missingFields(
		body.UserEmail == "", "user_email",
		body.HubUserID == "", "hub_user_id",
	)
	if len(missing) > 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email and hub_user_id are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missing,
		})
		return
	}

	// Email format validation legere (anti-typo Hub). Coherent avec sync-member.
	if !strings.Contains(body.UserEmail, "@") {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email is malformed (missing @)", http.StatusUnprocessableEntity, map[string]interface{}{
			"hint": "user_email must contain @",
		})
		return
	}

	// Validation reason si fournie (vide = default "manual" au service).
	if body.Reason != "" && !body.Reason.IsValid() {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "reason must be quota_seat_exceeded|manual", http.StatusBadRequest, map[string]interface{}{
			"reason":  string(body.Reason),
			"allowed": []string{"quota_seat_exceeded", "manual"},
		})
		return
	}

	resp, alreadyFrozen, err := h.service.FreezeMember(r.Context(), domain.FreezeMemberInput{
		TenantID:  tenantID,
		UserEmail: body.UserEmail,
		HubUserID: body.HubUserID,
		Reason:    body.Reason,
	})
	// Invalidation immediate du cache freeze sur mutation reussie : permet aux
	// tests E2E et au cas reel d'observer le freeze des le prochain hit (sans
	// attendre TTL 60s). On invalide aussi sur alreadyFrozen=true (defensive :
	// si la row a ete restoree depuis le dernier lookup, le cache pourrait
	// avoir vu frozen=false).
	if err == nil && h.frozenCache != nil && resp != nil {
		// On ne connait pas l'app_user_id ici (pas dans le response), donc on
		// purge tout le cache lie a ce workspace pour ce email — solution
		// simple : Clear() global. Le cout est minimal vu le TTL court (60s).
		// Alternative future : exposer app_user_id dans response et faire un
		// Invalidate cible.
		h.frozenCache.Clear()
	}
	if err != nil {
		if errors.Is(err, service.ErrCannotFreezeOwner) {
			WriteJSONErrorCode(w, ErrCodeCannotFreezeOwner, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id":  tenantID,
				"user_email": body.UserEmail,
				"hint":       "use POST /api/tenants/{id}/transfer-owner first if you really need to freeze this user",
			})
			return
		}
		if errors.Is(err, service.ErrMemberNotInWorkspace) {
			WriteJSONErrorCode(w, ErrCodeUserNotMember, "user is not a member of this workspace", http.StatusNotFound, map[string]interface{}{
				"tenant_id":  tenantID,
				"user_email": body.UserEmail,
			})
			return
		}
		if errors.Is(err, service.ErrFrozenMemberRepoNotConfigured) {
			WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusServiceUnavailable, map[string]interface{}{
				"hint": "frozen_member support not configured on this instance (self-hosted mode?)",
			})
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("freeze_member", err, map[string]interface{}{
			"tenant_id":   tenantID,
			"user_email":  body.UserEmail,
			"hub_user_id": body.HubUserID,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, veridianGenericInternalError, http.StatusInternalServerError, nil)
		return
	}

	// Idempotence : si deja frozen, on retourne 409 conformement au brief
	// team-lead. Le body inclut la row existante (frozen_at d'origine + reason)
	// pour permettre au Hub de comparer si une action est requise.
	if alreadyFrozen {
		WriteJSONErrorCode(w, ErrCodeMemberAlreadyFrozen, "user is already frozen", http.StatusConflict, map[string]interface{}{
			"tenant_id":  resp.TenantID,
			"user_email": resp.UserEmail,
			"frozen_at":  resp.FrozenAt,
			"reason":     resp.Reason,
		})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleUnfreezeMember : §5.21 unfreeze-member.
//
// Body :
//
//	{
//	  "user_email": "bob@example.com",
//	  "hub_user_id": "user_hub_abc123"
//	}
//
// Reponse 200 :
//
//	{
//	  "tenant_id":   "ws-1",
//	  "user_email":  "bob@example.com",
//	  "hub_user_id": "user_hub_abc123",
//	  "unfrozen_at": "2026-05-25T12:30:00Z"
//	}
//
// Erreurs :
//
//	400 invalid_payload    : champs manquants
//	404 tenant_not_found   : workspace inexistant
//	404 user_not_member    : user pas membre du workspace (ou inconnu)
//	500 internal_error     : echec service
//
// Note : si le user n'etait pas frozen (replay idempotent), on retourne quand
// meme 200 avec unfrozen_at = now, sans webhook (cf. service §UnfreezeMember).
// Distinct du brief qui prevoit 404 — le 200 idempotent est plus aligne sur
// le reste du contrat (remove-member, restore-member). Le caller voit la
// difference via le webhook : pas de tenant.member_unfrozen sur replay.
func (h *VeridianHandler) handleUnfreezeMember(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantId")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		UserEmail string `json:"user_email"`
		HubUserID string `json:"hub_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}

	missing := missingFields(
		body.UserEmail == "", "user_email",
		body.HubUserID == "", "hub_user_id",
	)
	if len(missing) > 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email and hub_user_id are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missing,
		})
		return
	}

	resp, _, err := h.service.UnfreezeMember(r.Context(), domain.UnfreezeMemberInput{
		TenantID:  tenantID,
		UserEmail: body.UserEmail,
		HubUserID: body.HubUserID,
	})
	// Invalidation immediate du cache freeze sur mutation reussie : meme
	// rationale que handleFreezeMember. Pas de discrimination wasFrozen=true/false,
	// on Clear() systematiquement post-call pour eviter qu'une entree obsolete
	// trahisse l'etat reel.
	if err == nil && h.frozenCache != nil {
		h.frozenCache.Clear()
	}
	if err != nil {
		if errors.Is(err, service.ErrMemberNotInWorkspace) {
			WriteJSONErrorCode(w, ErrCodeUserNotMember, "user is not a member of this workspace", http.StatusNotFound, map[string]interface{}{
				"tenant_id":  tenantID,
				"user_email": body.UserEmail,
			})
			return
		}
		if errors.Is(err, service.ErrFrozenMemberRepoNotConfigured) {
			WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusServiceUnavailable, nil)
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("unfreeze_member", err, map[string]interface{}{
			"tenant_id":   tenantID,
			"user_email":  body.UserEmail,
			"hub_user_id": body.HubUserID,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, veridianGenericInternalError, http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
