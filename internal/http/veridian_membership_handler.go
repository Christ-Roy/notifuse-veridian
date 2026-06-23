package http

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
//
// Handlers HTTP pour les endpoints CONTRAT-HUB §5.18 (sync-member),
// §5.19 (remove-member), §5.20 (restore-member). Voir
// todo/2026-05-19-v13-multi-membre-cross-app.md et veridian_membership.go.
//
// Routes (enregistrees dans VeridianHandler.RegisterRoutes) :
//
//	POST /api/tenants/{id}/sync-member     -> SyncMemberResponse
//	POST /api/tenants/{id}/remove-member   -> RemoveMemberResponse
//	POST /api/tenants/{id}/restore-member  -> RestoreMemberResponse
//
// Auth : HMAC (X-Veridian-Hub-Signature) + Idempotency-Key (cf. helper
// writeRoute dans veridian_handler.go).
//
// freeze/unfreeze members (§5.21) : non livre v1.3 — exige refonte paywall
// per-user (middleware tenant-level aujourd'hui). Ticket de suivi a creer
// si Robert souhaite l'activer une fois le quota seats live cote Hub.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

// handleSyncMember : §5.18.3 sync-member.
//
// Body :
//
//	{
//	  "user_email": "alice@example.com",
//	  "hub_user_id": "user_hub_abc123",
//	  "role": "member" | "admin",
//	  "invited_at": "2026-05-19T12:00:00Z",  // optionnel, audit
//	  "joined_at":  "2026-05-19T12:05:00Z"   // optionnel, audit
//	}
//
// Reponse 200 :
//
//	{
//	  "tenant_id":   "ws-1",
//	  "user_email":  "alice@example.com",
//	  "synced":      true,
//	  "app_user_id": "<uuid notifuse>",
//	  "app_role":    "member" | "owner"
//	}
//
// Erreurs :
//
//	400 invalid_payload  : champs manquants ou role invalide
//	404 tenant_not_found : workspace inexistant
//	422 invalid_payload  : email malforme (sans @)
//	500 internal_error   : echec service
func (h *VeridianHandler) handleSyncMember(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		UserEmail string                `json:"user_email"`
		HubUserID string                `json:"hub_user_id"`
		Role      domain.SyncMemberRole `json:"role"`
		InvitedAt string                `json:"invited_at,omitempty"`
		JoinedAt  string                `json:"joined_at,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}

	missing := missingFields(
		body.UserEmail == "", "user_email",
		body.HubUserID == "", "hub_user_id",
		string(body.Role) == "", "role",
	)
	if len(missing) > 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email, hub_user_id and role are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missing,
		})
		return
	}

	// Email format validation legere (anti-typo Hub). Coherent avec discovery.
	if !strings.Contains(body.UserEmail, "@") {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email is malformed (missing @)", http.StatusUnprocessableEntity, map[string]interface{}{
			"hint": "user_email must contain @",
		})
		return
	}

	if !body.Role.IsValid() {
		WriteJSONErrorCode(w, ErrCodeInvalidRole, "role must be member|admin", http.StatusBadRequest, map[string]interface{}{
			"role":    string(body.Role),
			"allowed": []string{"member", "admin"},
		})
		return
	}

	resp, err := h.service.SyncMember(r.Context(), domain.SyncMemberInput{
		TenantID:  tenantID,
		UserEmail: body.UserEmail,
		HubUserID: body.HubUserID,
		Role:      body.Role,
	})
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("sync_member", err, map[string]interface{}{
			"tenant_id":   tenantID,
			"hub_user_id": body.HubUserID,
		})
		// Message générique côté client : l'erreur brute (potentiellement DB/SQL)
		// est déjà loggée ci-dessus. Le code machine `internal_error` reste exposé
		// (le client Hub branche sa logique dessus, pas sur le texte).
		WriteJSONErrorCode(w, ErrCodeInternalError, "failed to sync member", http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleRemoveMember : §5.19.2 remove-member.
//
// Body :
//
//	{
//	  "user_email": "alice@example.com",
//	  "reason": "user_request" | "admin_action"  // optionnel, audit
//	}
//
// Reponse 200 :
//
//	{
//	  "tenant_id":   "ws-1",
//	  "user_email":  "alice@example.com",
//	  "removed_at":  "2026-05-19T12:00:00Z"
//	}
//
// Erreurs :
//
//	400 invalid_payload     : user_email manquant
//	404 tenant_not_found    : workspace inexistant
//	409 cannot_remove_owner : target est l'owner (utiliser transfer-owner)
//	500 internal_error      : echec service
func (h *VeridianHandler) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		UserEmail string `json:"user_email"`
		Reason    string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if body.UserEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"user_email"},
		})
		return
	}

	resp, err := h.service.RemoveMember(r.Context(), domain.RemoveMemberInput{
		TenantID:  tenantID,
		UserEmail: body.UserEmail,
		Reason:    body.Reason,
	})
	if err != nil {
		if errors.Is(err, service.ErrCannotRemoveOwner) {
			WriteJSONErrorCode(w, ErrCodeCannotRemoveOwner, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id":  tenantID,
				"user_email": body.UserEmail,
				"hint":       "use POST /api/tenants/{id}/transfer-owner to change owner first",
			})
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("remove_member", err, map[string]interface{}{
			"tenant_id":  tenantID,
			"user_email": body.UserEmail,
		})
		// Message générique : l'erreur brute est déjà loggée, le code reste exposé.
		WriteJSONErrorCode(w, ErrCodeInternalError, "failed to remove member", http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleRestoreMember : §5.20 restore-member.
//
// Body :
//
//	{"user_email": "alice@example.com"}
//
// Reponse 200 :
//
//	{
//	  "tenant_id":   "ws-1",
//	  "user_email":  "alice@example.com",
//	  "restored_at": "2026-05-19T12:00:00Z"
//	}
//
// Erreurs :
//
//	400 invalid_payload  : user_email manquant
//	404 tenant_not_found : workspace inexistant
//	500 internal_error   : echec service
func (h *VeridianHandler) handleRestoreMember(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		UserEmail string `json:"user_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if body.UserEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "user_email is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"user_email"},
		})
		return
	}

	resp, err := h.service.RestoreMember(r.Context(), domain.RestoreMemberInput{
		TenantID:  tenantID,
		UserEmail: body.UserEmail,
	})
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("restore_member", err, map[string]interface{}{
			"tenant_id":  tenantID,
			"user_email": body.UserEmail,
		})
		// Message générique : l'erreur brute est déjà loggée, le code reste exposé.
		WriteJSONErrorCode(w, ErrCodeInternalError, "failed to restore member", http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
