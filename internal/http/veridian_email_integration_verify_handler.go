package http

// POST /api/veridian/admin/email-integration.verify (HMAC Hub, idempotency).
// Body : {workspace_id, integration_id, to}. Exécute le vrai test de transport
// vers `to` puis pose VeridianTransportVerifiedAt. Réponse : {verified, ...}
// sans aucun secret. Jamais accessible à un owner/member (pas de JWT ici).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
)

// VeridianEmailIntegrationVerifier : capacité consommée par le handler,
// implémentée par *service.EmailService.
type VeridianEmailIntegrationVerifier interface {
	VeridianAdminVerifyEmailIntegration(ctx context.Context, workspaceID, integrationID, to string) error
}

// SetEmailIntegrationVerifier injecte le vérificateur. nil => 503.
func (h *VeridianHandler) SetEmailIntegrationVerifier(v VeridianEmailIntegrationVerifier) {
	h.emailVerifier = v
}

type veridianEmailIntegrationVerifyRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	IntegrationID string `json:"integration_id"`
	To            string `json:"to"`
}

func (h *VeridianHandler) handleEmailIntegrationVerify(w http.ResponseWriter, r *http.Request) {
	if h.emailVerifier == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "email integration verification unavailable", http.StatusServiceUnavailable, nil)
		return
	}
	var req veridianEmailIntegrationVerifyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if err := h.emailVerifier.VeridianAdminVerifyEmailIntegration(r.Context(), req.WorkspaceID, req.IntegrationID, req.To); err != nil {
		var validationErr domain.ValidationError
		if errors.As(err, &validationErr) {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, validationErr.Message, http.StatusBadRequest, nil)
			return
		}
		h.logError("email_integration_verify", err, map[string]interface{}{
			"workspace_id":   req.WorkspaceID,
			"integration_id": req.IntegrationID,
		})
		// Transport refuse : NON verifie. Message generique (pas de fuite de
		// l'erreur SMTP brute, deja loggee cote serveur).
		WriteJSONErrorCode(w, ErrCodeInternalError, "transport test failed: integration NOT verified", http.StatusBadGateway, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"verified":       true,
		"workspace_id":   req.WorkspaceID,
		"integration_id": req.IntegrationID,
	})
}
