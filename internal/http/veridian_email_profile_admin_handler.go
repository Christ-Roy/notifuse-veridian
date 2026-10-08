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

// VeridianEmailProfileAdminHandler sert (lot 4, 08/10/2026) :
//
//	POST /api/veridian/emailProfiles.setUsage  {workspace_id, integration_id, usage}
//	POST /api/veridian/emailProfiles.pause     {workspace_id, integration_id}
//	POST /api/veridian/emailProfiles.resume    {workspace_id, integration_id}
//
// Droit requis (vérifié par le service) : écriture sur le workspace. Les refus de
// règle (profil non vérifié, rotation vidée, pause d'un transactionnel...) sont des
// 400 avec un message lisible. La réponse ne contient que des identifiants.
type VeridianEmailProfileAdminHandler struct {
	service      domain.VeridianEmailProfileAdminService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianEmailProfileAdminHandler(service domain.VeridianEmailProfileAdminService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianEmailProfileAdminHandler {
	return &VeridianEmailProfileAdminHandler{service: service, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianEmailProfileAdminHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	mux.Handle("POST /api/veridian/emailProfiles.setUsage", requireAuth(nethttp.HandlerFunc(h.handleSetUsage)))
	mux.Handle("POST /api/veridian/emailProfiles.pause", requireAuth(nethttp.HandlerFunc(h.handlePause)))
	mux.Handle("POST /api/veridian/emailProfiles.resume", requireAuth(nethttp.HandlerFunc(h.handleResume)))
}

func (h *VeridianEmailProfileAdminHandler) decode(w nethttp.ResponseWriter, r *nethttp.Request, into interface{}) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		WriteJSONError(w, "invalid body", nethttp.StatusBadRequest)
		return false
	}
	if err := json.Unmarshal(raw, into); err != nil {
		WriteJSONError(w, "invalid JSON body", nethttp.StatusBadRequest)
		return false
	}
	return true
}

func (h *VeridianEmailProfileAdminHandler) fail(w nethttp.ResponseWriter, workspaceID, what string, err error) {
	var validationErr domain.ValidationError
	if errors.As(err, &validationErr) {
		WriteJSONError(w, validationErr.Message, nethttp.StatusBadRequest)
		return
	}
	h.logger.WithField("workspace_id", workspaceID).WithField("error", err.Error()).Error("Failed to " + what)
	WriteAuthAwareError(w, err, "Failed to "+what, nethttp.StatusInternalServerError)
}

func (h *VeridianEmailProfileAdminHandler) handleSetUsage(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req domain.VeridianSetUsageRequest
	if !h.decode(w, r, &req) {
		return
	}
	result, err := h.service.SetUsage(r.Context(), req)
	if err != nil {
		h.fail(w, req.WorkspaceID, "set email profile usage", err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *VeridianEmailProfileAdminHandler) handlePause(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req domain.VeridianPauseRequest
	if !h.decode(w, r, &req) {
		return
	}
	result, err := h.service.Pause(r.Context(), req)
	if err != nil {
		h.fail(w, req.WorkspaceID, "pause email profile", err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}

func (h *VeridianEmailProfileAdminHandler) handleResume(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req domain.VeridianPauseRequest
	if !h.decode(w, r, &req) {
		return
	}
	result, err := h.service.Resume(r.Context(), req)
	if err != nil {
		h.fail(w, req.WorkspaceID, "resume email profile", err)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}
