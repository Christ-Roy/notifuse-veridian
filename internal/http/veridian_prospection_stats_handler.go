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

// VeridianProspectionStatsHandler sert GET|POST /api/veridian/prospection.stats
// (lot 5, 08/10/2026) : réponses par séquence et par liste, avancement des séquences,
// stock par liste. Lecture seule ; la fenêtre start/end suit la même règle que
// messages.replyStats (date nue = jour de fin inclus). Les permissions sont vérifiées
// par le service.
type VeridianProspectionStatsHandler struct {
	service      domain.VeridianProspectionStatsService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianProspectionStatsHandler(service domain.VeridianProspectionStatsService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianProspectionStatsHandler {
	return &VeridianProspectionStatsHandler{service: service, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianProspectionStatsHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	handler := requireAuth(nethttp.HandlerFunc(h.handle))
	mux.Handle("GET /api/veridian/prospection.stats", handler)
	mux.Handle("POST /api/veridian/prospection.stats", handler)
}

func (h *VeridianProspectionStatsHandler) handle(w nethttp.ResponseWriter, r *nethttp.Request) {
	req, err := h.parseRequest(r)
	if err != nil {
		WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	stats, err := h.service.GetProspectionStats(r.Context(), req)
	if err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to compute prospection stats")
		WriteAuthAwareError(w, err, "Failed to compute prospection stats", nethttp.StatusInternalServerError)
		return
	}
	writeJSON(w, nethttp.StatusOK, stats)
}

func (h *VeridianProspectionStatsHandler) parseRequest(r *nethttp.Request) (*domain.VeridianProspectionStatsRequest, error) {
	raw := veridianReplyStatsRawRequest{}
	if r.Method == nethttp.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, errors.New("failed to read request body")
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, errors.New("invalid JSON body")
			}
		}
	}
	query := r.URL.Query()
	if v := query.Get("workspace_id"); v != "" {
		raw.WorkspaceID = v
	}
	if v := query.Get("start"); v != "" {
		raw.Start = v
	}
	if v := query.Get("end"); v != "" {
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
	return &domain.VeridianProspectionStatsRequest{WorkspaceID: raw.WorkspaceID, Since: since, Until: until}, nil
}
