package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianEmailProfileVerifyHandler sert POST /api/veridian/emailProfiles.verify (fiche 79) :
// la verification d'un profil d'envoi par l'utilisateur du workspace (JWT, cle API comprise).
// Droit et isolation : verifies par le service. La reponse ne contient aucun secret ; un echec
// de transport est un 502 generique (l'erreur SMTP brute reste dans le journal serveur).
type VeridianEmailProfileVerifyHandler struct {
	service interface {
		VerifyEmailProfile(ctx context.Context, workspaceID, integrationID, to string) error
	}
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianEmailProfileVerifyHandler(svc interface {
	VerifyEmailProfile(ctx context.Context, workspaceID, integrationID, to string) error
}, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianEmailProfileVerifyHandler {
	return &VeridianEmailProfileVerifyHandler{service: svc, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianEmailProfileVerifyHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	mux.Handle("POST /api/veridian/emailProfiles.verify", requireAuth(nethttp.HandlerFunc(h.handle)))
}

type veridianEmailProfileVerifyRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	IntegrationID string `json:"integration_id"`
	To            string `json:"to"`
}

func (h *VeridianEmailProfileVerifyHandler) handle(w nethttp.ResponseWriter, r *nethttp.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		WriteJSONError(w, "invalid body", nethttp.StatusBadRequest)
		return
	}
	var req veridianEmailProfileVerifyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		WriteJSONError(w, "invalid JSON body", nethttp.StatusBadRequest)
		return
	}
	if err := h.service.VerifyEmailProfile(r.Context(), req.WorkspaceID, req.IntegrationID, req.To); err != nil {
		var validationErr domain.ValidationError
		switch {
		case errors.As(err, &validationErr):
			WriteJSONError(w, validationErr.Message, nethttp.StatusBadRequest)
		case errors.Is(err, service.ErrVeridianVerifyRateLimited):
			WriteJSONError(w, err.Error(), nethttp.StatusTooManyRequests)
		default:
			if message, status, ok := classifyAuthError(err); ok {
				WriteJSONError(w, message, status)
				return
			}
			h.logger.WithField("workspace_id", req.WorkspaceID).WithField("integration_id", req.IntegrationID).WithField("error", err.Error()).Error("Email profile verification failed")
			WriteJSONError(w, "transport test failed: integration NOT verified", nethttp.StatusBadGateway)
		}
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]interface{}{
		"verified": true, "workspace_id": req.WorkspaceID, "integration_id": req.IntegrationID,
	})
}
