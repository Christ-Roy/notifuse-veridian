package http

import (
	"encoding/json"
	"io"
	nethttp "net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianEmailProfileOverviewHandler sert GET|POST /api/veridian/emailProfiles.overview.
// Lecture seule, accessible avec une clé API scopée au workspace (RequireAuth
// accepte le jeton de clé API ; la permission de lecture est vérifiée par le service).
type VeridianEmailProfileOverviewHandler struct {
	service      domain.VeridianEmailProfileOverviewService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianEmailProfileOverviewHandler(service domain.VeridianEmailProfileOverviewService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianEmailProfileOverviewHandler {
	return &VeridianEmailProfileOverviewHandler{service: service, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianEmailProfileOverviewHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	handler := requireAuth(nethttp.HandlerFunc(h.handle))
	mux.Handle("GET /api/veridian/emailProfiles.overview", handler)
	mux.Handle("POST /api/veridian/emailProfiles.overview", handler)
}

func (h *VeridianEmailProfileOverviewHandler) handle(w nethttp.ResponseWriter, r *nethttp.Request) {
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
	result, err := h.service.GetEmailProfilesOverview(r.Context(), workspaceID)
	if err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to compute email profiles overview")
		WriteAuthAwareError(w, err, "Failed to compute email profiles overview", nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, result)
}
