package http

// === Veridian patch — Lot K (2026-05-21) ===
// Handlers HTTP pour les endpoints CONTRAT-HUB §5.15 (rotate-api-key) et
// §5.16 (transfer-owner). Voir ticket
// todo/2026-05-19-rotate-transfer-owner-endpoints.md.
//
// Routes (enregistrees dans VeridianHandler.RegisterRoutes) :
//
//	POST /api/tenants/{id}/rotate-api-key   -> RotateAPIKeyResponse
//	POST /api/tenants/{id}/transfer-owner   -> TransferOwnerResponse
//
// Auth : HMAC (X-Veridian-Hub-Signature) + Idempotency-Key (cf. helper
// writeRoute dans veridian_handler.go).

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

// handleRotateAPIKey : §5.15 rotate-api-key.
//
// Body :
//
//	{"reason": "string"}     (required, audit GDPR)
//
// Reponse 200 :
//
//	{
//	  "tenant_id": "ws-1",
//	  "new_api_key": "<jwt token>",
//	  "new_api_key_email": "veridian-api-ws-1-r12345@notifuse.app.veridian.site",
//	  "old_api_key_revokes_at": "2026-05-21T12:05:00Z"
//	}
//
// Erreurs :
//
//	400 invalid_payload  : body manquant ou reason vide
//	404 tenant_not_found : workspace inexistant
//	503 grace_unavailable: apiKeyGraceRepo non configure (mode self-hosted)
//	500 internal_error   : echec service
func (h *VeridianHandler) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body == nil || r.ContentLength == 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "body required with reason field", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"reason"},
		})
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if body.Reason == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "reason is required (audit GDPR)", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"reason"},
		})
		return
	}

	resp, err := h.service.RotateAPIKey(r.Context(), domain.RotateAPIKeyInput{
		TenantID: tenantID,
		Reason:   body.Reason,
	})
	if err != nil {
		// 503 si grace repo absent (mode self-hosted).
		if errors.Is(err, service.ErrAPIKeyGraceRepoNotConfigured) {
			WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "api_key grace tracking not configured on this instance", http.StatusServiceUnavailable, nil)
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("rotate_api_key", err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleTransferOwner : §5.16 transfer-owner.
//
// Body :
//
//	{
//	  "new_owner_email": "newowner@x.test",
//	  "reason": "string"
//	}
//
// Reponse 200 :
//
//	{
//	  "tenant_id": "ws-1",
//	  "old_owner": "old@x.test",
//	  "new_owner": "newowner@x.test",
//	  "transferred_at": "2026-05-21T12:00:00Z"
//	}
//
// Erreurs :
//
//	400 invalid_payload  : body manquant, new_owner_email ou reason vide
//	404 tenant_not_found : workspace inexistant
//	500 internal_error   : echec service
func (h *VeridianHandler) handleTransferOwner(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		NewOwnerEmail string `json:"new_owner_email"`
		Reason        string `json:"reason"`
	}
	if r.Body == nil || r.ContentLength == 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "body required with new_owner_email and reason fields", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"new_owner_email", "reason"},
		})
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	missing := missingFields(
		body.NewOwnerEmail == "", "new_owner_email",
		body.Reason == "", "reason",
	)
	if len(missing) > 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "new_owner_email and reason are required (audit GDPR)", http.StatusBadRequest, map[string]interface{}{
			"missing": missing,
		})
		return
	}

	resp, err := h.service.TransferOwner(r.Context(), domain.TransferOwnerInput{
		TenantID:      tenantID,
		NewOwnerEmail: body.NewOwnerEmail,
		Reason:        body.Reason,
	})
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("transfer_owner", err, map[string]interface{}{
			"tenant_id":       tenantID,
			"new_owner_email": body.NewOwnerEmail,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
