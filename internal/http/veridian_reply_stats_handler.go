package http

// === Veridian patch ===
// Handler du KPI reply rate (taux de réponse cold outbound, ticket
// todo/2026-06-16-kpi-reply-rate-dashboard.md).
//
// Route POST + GET /api/veridian/messages.replyStats — auth JWT console
// (RequireAuth), gardien d'appartenance workspace + permission contacts:read
// dans le service (même posture que le breakdown contacts R1).
//
// ⚠️ POST ET GET routés explicitement : Go 1.22+ exige la méthode dans le
// pattern. Un endpoint routé sur une seule méthode laisse l'autre tomber dans
// le catchall SPA de root_handler.go (HTML 200 trompeur, zéro log) — piège P0
// vécu 2026-05-25, cf. CLAUDE.md "Pièges historiques".
//
// CHOIX D'ARCHITECTURE — endpoint dédié, PAS de greffe dans l'analytics : la
// donnée reply vit dans la table SÉPARÉE veridian_contact_reply (pas dans
// message_history que le moteur analytics générique interroge). Cf.
// internal/domain/veridian_reply_stats.go.
//
// Réponse : {"replied": N}. Le ratio replied/sent est calculé côté front avec
// le count_sent qu'EmailMetricsChart charge déjà (pas de 2e source de vérité).
//
// Fenêtre : start/end (ISO date YYYY-MM-DD ou RFC3339). end est traité comme
// borne EXCLUSIVE au DÉBUT DU JOUR SUIVANT pour une date nue (YYYY-MM-DD = minuit),
// afin que le jour de fin soit INCLUS — aligné sur le dateRange [start, end]
// inclusif du dashboard analytics.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianReplyStatsHandler expose le compte de réponses (KPI reply rate).
type VeridianReplyStatsHandler struct {
	service      domain.VeridianReplyStatsService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianReplyStatsHandler construit le handler.
func NewVeridianReplyStatsHandler(
	service domain.VeridianReplyStatsService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianReplyStatsHandler {
	return &VeridianReplyStatsHandler{
		service:      service,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes enregistre POST + GET sur la même URL, protégés par RequireAuth.
func (h *VeridianReplyStatsHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	handler := requireAuth(http.HandlerFunc(h.handleReplyStats))
	mux.Handle("POST /api/veridian/messages.replyStats", handler)
	mux.Handle("GET /api/veridian/messages.replyStats", handler)
}

func (h *VeridianReplyStatsHandler) handleReplyStats(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseRequest(r)
	if err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	stats, err := h.service.GetReplyStats(r.Context(), req)
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
		h.logger.WithField("error", err.Error()).Error("Failed to compute reply stats")
		WriteJSONError(w, "Failed to compute reply stats", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// veridianReplyStatsRawRequest est la forme brute du body/query (dates ISO en
// string) avant résolution en time.Time.
type veridianReplyStatsRawRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Start       string `json:"start"`
	End         string `json:"end"`
}

// parseRequest lit la requête depuis le body JSON (POST) ou la query (GET) et
// résout start/end (ISO) en bornes [Since, Until[.
func (h *VeridianReplyStatsHandler) parseRequest(r *http.Request) (*domain.VeridianReplyStatsRequest, error) {
	raw := veridianReplyStatsRawRequest{}

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

	// La query string surcharge / complète (utile pour GET, et tolérant pour un
	// POST sans body).
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

	return &domain.VeridianReplyStatsRequest{
		WorkspaceID: raw.WorkspaceID,
		Since:       since,
		Until:       until,
	}, nil
}

// veridianParseStatsDate résout une date ISO (YYYY-MM-DD ou RFC3339) en UTC.
// Vide → zero time (pas de borne). endExclusive=true sur une date NUE
// (YYYY-MM-DD) ajoute un jour pour rendre la borne haute exclusive au lendemain,
// de sorte que le jour de fin soit INCLUS (aligné sur le dateRange inclusif du
// dashboard analytics). Sur un RFC3339 (instant précis) on ne décale pas.
func veridianParseStatsDate(s string, endExclusive bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	// RFC3339 (instant précis) : on prend tel quel, en UTC.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	// Date nue YYYY-MM-DD : minuit UTC.
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	t = t.UTC()
	if endExclusive {
		t = t.AddDate(0, 0, 1) // jour de fin inclus → borne exclusive au lendemain
	}
	return t, nil
}
