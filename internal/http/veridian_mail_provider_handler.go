package http

// === Veridian patch — Mail provider choice per workspace (V48, 2026-05-25) ===
//
// Handlers HTTP pour les endpoints :
//
//	POST /api/workspaces/{id}/mail-provider-choice
//	GET  /api/workspaces/{id}/mail-provider-choice
//
// Auth : HMAC (X-Veridian-Hub-Signature). POST passe aussi par le middleware
// Idempotency (via writeRoute wrapper). Le GET est read-only, HMAC seul.
//
// Voir todo/2026-05-25-mail-send-as-user-via-hub-gateway.md §3.4,
// domain/veridian_mail_provider.go, service/veridian_mail_provider_service.go.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/service"
)

// handleSetMailProviderChoice : POST /api/workspaces/{id}/mail-provider-choice.
//
// Body :
//
//	{ "choice": "smtp_generic" | "hub_gmail" }
//
// Reponse 200 :
//
//	{
//	  "workspace_id": "ws-1",
//	  "choice":       "hub_gmail",
//	  "updated_at":   "2026-05-25T12:00:00Z"
//	}
//
// Erreurs :
//
//	400 invalid_payload      : id path manquant, JSON invalide, choice manquant
//	400 invalid_payload      : choice hors enum {'smtp_generic','hub_gmail'}
//	404 workspace_not_found  : workspace inconnu
//	503 service_unavailable  : mailProviderService pas configure (self-hosted)
//	500 internal_error       : autre echec
func (h *VeridianHandler) handleSetMailProviderChoice(w http.ResponseWriter, r *http.Request) {
	if h.mailProviderService == nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "mail provider service not configured", http.StatusServiceUnavailable, map[string]interface{}{
			"hint": "mail provider service requires SetMailProviderService injection (V48 not wired)",
		})
		return
	}

	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "workspace id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"id"},
		})
		return
	}

	var body struct {
		Choice domain.MailProviderChoice `json:"choice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if body.Choice == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "choice is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"choice"},
			"allowed": []string{string(domain.MailProviderSMTPGeneric), string(domain.MailProviderHubGmail)},
		})
		return
	}

	resp, err := h.mailProviderService.SetMailProviderChoice(r.Context(), workspaceID, body.Choice)
	if err != nil {
		h.logError("set_mail_provider_choice", err, map[string]interface{}{
			"workspace_id": workspaceID,
			"choice":       string(body.Choice),
		})
		if errors.Is(err, service.ErrInvalidMailProviderChoice) {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, err.Error(), http.StatusBadRequest, map[string]interface{}{
				"allowed": []string{string(domain.MailProviderSMTPGeneric), string(domain.MailProviderHubGmail)},
				"got":     string(body.Choice),
			})
			return
		}
		if errors.Is(err, repository.ErrWorkspaceNotFoundForMailProvider) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "workspace not found", http.StatusNotFound, map[string]interface{}{
				"workspace_id": workspaceID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetMailProviderChoice : GET /api/workspaces/{id}/mail-provider-choice.
//
// Reponse 200 :
//
//	{
//	  "workspace_id": "ws-1",
//	  "choice":       "smtp_generic"
//	}
//
// Erreurs :
//
//	400 invalid_payload      : id path manquant
//	503 service_unavailable  : mailProviderService pas configure
//	500 internal_error       : erreur repo
//
// Note : pas de 404 ici — le repo retourne MailProviderSMTPGeneric comme
// fallback safe si le workspace est absent (semantique read-only "qu'est-ce
// que ce workspace utiliserait ?"). Si le caller a besoin de distinguer
// "workspace absent", il consulte GET /api/tenants/{id}/status d'abord.
func (h *VeridianHandler) handleGetMailProviderChoice(w http.ResponseWriter, r *http.Request) {
	if h.mailProviderService == nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "mail provider service not configured", http.StatusServiceUnavailable, map[string]interface{}{
			"hint": "mail provider service requires SetMailProviderService injection (V48 not wired)",
		})
		return
	}

	workspaceID := r.PathValue("id")
	if workspaceID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "workspace id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"id"},
		})
		return
	}

	choice, err := h.mailProviderService.GetMailProviderChoice(r.Context(), workspaceID)
	if err != nil {
		h.logError("get_mail_provider_choice", err, map[string]interface{}{
			"workspace_id": workspaceID,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, domain.MailProviderChoiceResponse{
		WorkspaceID: workspaceID,
		Choice:      choice,
		// UpdatedAt vide (omitempty) sur GET.
	})
}
