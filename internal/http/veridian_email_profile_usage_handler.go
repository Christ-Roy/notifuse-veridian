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

type VeridianEmailProfileUsageHandler struct {
	service      domain.VeridianEmailProfileUsageService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianEmailProfileUsageHandler(service domain.VeridianEmailProfileUsageService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianEmailProfileUsageHandler {
	return &VeridianEmailProfileUsageHandler{service: service, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianEmailProfileUsageHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	handler := requireAuth(nethttp.HandlerFunc(h.handle))
	mux.Handle("GET /api/veridian/emailProfiles.usage", handler)
	mux.Handle("POST /api/veridian/emailProfiles.usage", handler)
}

func (h *VeridianEmailProfileUsageHandler) handle(w nethttp.ResponseWriter, r *nethttp.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if r.Method == nethttp.MethodPost {
		var body struct {
			WorkspaceID string `json:"workspace_id"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || (len(raw) > 0 && json.Unmarshal(raw, &body) != nil) {
			WriteJSONError(w, "invalid JSON body", nethttp.StatusBadRequest)
			return
		}
		if workspaceID == "" {
			workspaceID = body.WorkspaceID
		}
	}
	if workspaceID == "" {
		WriteJSONError(w, "workspace_id is required", nethttp.StatusBadRequest)
		return
	}
	result, err := h.service.GetEmailProfilesUsage(r.Context(), workspaceID)
	if err != nil {
		var permissionErr *domain.PermissionError
		if errors.As(err, &permissionErr) {
			WriteJSONError(w, permissionErr.Error(), nethttp.StatusForbidden)
			return
		}
		if isAuthFailure(err) {
			WriteJSONError(w, "Unauthorized", nethttp.StatusUnauthorized)
			return
		}
		h.logger.WithField("error", err.Error()).Error("Failed to compute email profile usage")
		WriteJSONError(w, "Failed to compute email profile usage", nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}
