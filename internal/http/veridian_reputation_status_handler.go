package http

// === Veridian patch — fusible de réputation, API de lecture (2026-09-29) ===
//
// Route POST + GET /api/veridian/messages.reputationStatus — auth JWT console
// (RequireAuth), gardien d'appartenance workspace + permission
// message_history:read dans le service (même posture que le reply rate / le
// breakdown / l'engagement par classe).
//
// ⚠️ POST ET GET routés explicitement : Go 1.22+ exige la méthode dans le
// pattern. Un endpoint routé sur une seule méthode laisse l'autre tomber dans
// le catchall SPA de root_handler.go (HTML 200 trompeur, zéro log) — piège P0
// vécu 2026-05-25, cf. CLAUDE.md "Pièges historiques".
//
// C'est le signal "visible dans l'interface ou l'API" exigé par la mission qui
// a introduit le fusible de réputation (internal/service/queue/
// veridian_reputation_gate.go) : {"integrations":[{"integration_id",
// "sender_domain","sent_7d","hard_bounces_7d","hard_bounce_rate","threshold",
// "complaints_7d","frozen","frozen_reason"},...],"any_frozen":bool}.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianReputationStatusHandler expose l'état du fusible de réputation.
type VeridianReputationStatusHandler struct {
	service      domain.VeridianReputationStatusService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianReputationStatusHandler construit le handler.
func NewVeridianReputationStatusHandler(
	service domain.VeridianReputationStatusService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianReputationStatusHandler {
	return &VeridianReputationStatusHandler{
		service:      service,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes enregistre POST + GET sur la même URL, protégés par RequireAuth.
func (h *VeridianReputationStatusHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	handler := requireAuth(http.HandlerFunc(h.handleReputationStatus))
	mux.Handle("POST /api/veridian/messages.reputationStatus", handler)
	mux.Handle("GET /api/veridian/messages.reputationStatus", handler)
}

func (h *VeridianReputationStatusHandler) handleReputationStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.URL.Query().Get("workspace_id")
	if workspaceID == "" && r.Method == http.MethodPost {
		// Tolère un body JSON {"workspace_id": "..."} pour rester symétrique
		// avec les autres endpoints veridian/messages.* (reply stats, engagement).
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
		if err == nil && len(body) > 0 {
			var raw struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if json.Unmarshal(body, &raw) == nil {
				workspaceID = raw.WorkspaceID
			}
		}
	}
	if workspaceID == "" {
		WriteJSONError(w, "workspace_id is required", http.StatusBadRequest)
		return
	}

	status, err := h.service.GetReputationStatus(r.Context(), &domain.VeridianReputationStatusRequest{WorkspaceID: workspaceID})
	if err != nil {
		var permErr *domain.PermissionError
		if errors.As(err, &permErr) {
			WriteJSONError(w, permErr.Error(), http.StatusForbidden)
			return
		}
		if isAuthFailure(err) {
			WriteJSONError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h.logger.WithField("error", err.Error()).Error("Failed to compute reputation status")
		WriteJSONError(w, "Failed to compute reputation status", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, status)
}
