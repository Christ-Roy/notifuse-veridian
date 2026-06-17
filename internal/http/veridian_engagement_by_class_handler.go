package http

// === Veridian patch ===
// Handler du KPI engagement par classe de provider destinataire (dashboard cold,
// ticket todo/2026-06-16-kpi-engagement-par-classe-provider.md).
//
// Route POST + GET /api/veridian/messages.engagementByClass — auth JWT console
// (RequireAuth), gardien d'appartenance workspace + permission contacts:read
// dans le service (même posture que le breakdown R1 / le reply stats).
//
// ⚠️ POST ET GET routés explicitement : Go 1.22+ exige la méthode dans le
// pattern. Un endpoint routé sur une seule méthode laisse l'autre tomber dans
// le catchall SPA de root_handler.go (HTML 200 trompeur, zéro log) — piège P0
// vécu 2026-05-25, cf. CLAUDE.md "Pièges historiques".
//
// CHOIX D'ARCHITECTURE — endpoint dédié, PAS de greffe dans l'analytics : la
// classe n'est PAS une dimension de message_history (décision Lot 4). Le repo
// agrège par DOMAINE, le service mappe domaine → classe en Go. Cf.
// internal/domain/veridian_engagement_by_class.go.
//
// Réponse : {"by_class":{"google":{sent,delivered,bounced,opened,clicked},...},"total":{...}}
//
// Fenêtre : start/end (ISO YYYY-MM-DD ou RFC3339), même sémantique que le reply
// stats (end inclusif → borne exclusive au lendemain pour une date nue).

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianEngagementByClassHandler expose l'engagement par classe.
type VeridianEngagementByClassHandler struct {
	service      domain.VeridianEngagementByClassService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianEngagementByClassHandler construit le handler.
func NewVeridianEngagementByClassHandler(
	service domain.VeridianEngagementByClassService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianEngagementByClassHandler {
	return &VeridianEngagementByClassHandler{
		service:      service,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes enregistre POST + GET sur la même URL, protégés par RequireAuth.
func (h *VeridianEngagementByClassHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	handler := requireAuth(http.HandlerFunc(h.handleEngagementByClass))
	mux.Handle("POST /api/veridian/messages.engagementByClass", handler)
	mux.Handle("GET /api/veridian/messages.engagementByClass", handler)
}

func (h *VeridianEngagementByClassHandler) handleEngagementByClass(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseRequest(r)
	if err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := h.service.GetEngagementByClass(r.Context(), req)
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
		h.logger.WithField("error", err.Error()).Error("Failed to compute engagement by class")
		WriteJSONError(w, "Failed to compute engagement by class", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// veridianEngagementByClassRawRequest est la forme brute du body/query.
type veridianEngagementByClassRawRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Start       string `json:"start"`
	End         string `json:"end"`
}

// parseRequest lit la requête depuis le body JSON (POST) ou la query (GET) et
// résout start/end (ISO) en bornes [Since, Until[ via veridianParseStatsDate
// (helper partagé avec le reply stats handler).
func (h *VeridianEngagementByClassHandler) parseRequest(r *http.Request) (*domain.VeridianEngagementByClassRequest, error) {
	raw := veridianEngagementByClassRawRequest{}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
		if err != nil {
			return nil, errors.New("failed to read request body")
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, errors.New("invalid JSON body")
			}
		}
	}

	if v := r.URL.Query().Get("workspace_id"); v != "" {
		raw.WorkspaceID = v
	}
	if v := r.URL.Query().Get("start"); v != "" {
		raw.Start = v
	}
	if v := r.URL.Query().Get("end"); v != "" {
		raw.End = v
	}

	if raw.WorkspaceID == "" {
		return nil, errors.New("workspace_id is required")
	}

	since, err := veridianParseStatsDate(raw.Start, false)
	if err != nil {
		return nil, errors.New("invalid start date")
	}
	until, err := veridianParseStatsDate(raw.End, true)
	if err != nil {
		return nil, errors.New("invalid end date")
	}

	return &domain.VeridianEngagementByClassRequest{
		WorkspaceID: raw.WorkspaceID,
		Since:       since,
		Until:       until,
	}, nil
}
