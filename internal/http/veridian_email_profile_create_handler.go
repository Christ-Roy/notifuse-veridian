package http

import (
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianEmailProfileCreateHandler sert POST /api/veridian/emailProfiles.create
// (lot 3, page Profils d'envoi). Réservé au propriétaire (vérifié par le
// service). La réponse ne contient que des identifiants : jamais de secret, et
// le corps de la requête n'est jamais journalisé.
type VeridianEmailProfileCreateHandler struct {
	service      domain.VeridianEmailProfileCreateService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianEmailProfileCreateHandler(service domain.VeridianEmailProfileCreateService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianEmailProfileCreateHandler {
	return &VeridianEmailProfileCreateHandler{service: service, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianEmailProfileCreateHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	mux.Handle("POST /api/veridian/emailProfiles.create", requireAuth(nethttp.HandlerFunc(h.handle)))
}

func (h *VeridianEmailProfileCreateHandler) handle(w nethttp.ResponseWriter, r *nethttp.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		WriteJSONError(w, "invalid body", nethttp.StatusBadRequest)
		return
	}
	var req domain.VeridianCreateEmailProfileRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		WriteJSONError(w, "invalid JSON body", nethttp.StatusBadRequest)
		return
	}
	result, err := h.service.CreateEmailProfile(r.Context(), req)
	if err != nil {
		var validationErr domain.ValidationError
		if errors.As(err, &validationErr) {
			WriteJSONError(w, validationErr.Message, nethttp.StatusBadRequest)
			return
		}
		h.logger.WithField("workspace_id", req.WorkspaceID).WithField("error", err.Error()).Error("Failed to create email profile")
		WriteAuthAwareError(w, err, "Failed to create email profile", nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusCreated, result)
}
