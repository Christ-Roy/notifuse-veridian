package http

// === Veridian patch ===
// Handler du score de délivrabilité (spam score) sur un template cold rendu
// (ticket todo/2026-06-15-linter-deliverabilite-spam-score-templates.md).
//
// Route POST + GET /api/veridian/templates.deliverabilityScore — auth JWT console
// (RequireAuth), gardien d'appartenance workspace + permission templates:read
// dans le service. Le scoring est INSTANTANÉ (package pur, zéro I/O).
//
// ⚠️ POST ET GET routés explicitement : Go 1.22+ exige la méthode dans le
// pattern. Un endpoint routé sur une seule méthode laisse l'autre tomber dans le
// catchall SPA de root_handler.go (HTML 200 trompeur, zéro log) — piège P0 vécu
// 2026-05-25, cf. CLAUDE.md "Pièges historiques". POST sert le call applicatif
// (RPC-style Notifuse, body JSON avec subject+body) ; GET sert le debug rapide
// (query params, corps courts).
//
// Réponse : pkg/veridian_deliverability.Result
//   {"score":N,"is_risky":bool,"mode":"strict|lenient|default","rules":[...],"summary":"..."}

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianDeliverabilityScoreHandler expose le scoring de délivrabilité.
type VeridianDeliverabilityScoreHandler struct {
	service      domain.VeridianDeliverabilityScoreService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianDeliverabilityScoreHandler construit le handler.
func NewVeridianDeliverabilityScoreHandler(
	service domain.VeridianDeliverabilityScoreService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianDeliverabilityScoreHandler {
	return &VeridianDeliverabilityScoreHandler{
		service:      service,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes enregistre POST + GET sur la même URL, protégés par RequireAuth.
func (h *VeridianDeliverabilityScoreHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	handler := requireAuth(http.HandlerFunc(h.handleScore))
	mux.Handle("POST /api/veridian/templates.deliverabilityScore", handler)
	mux.Handle("GET /api/veridian/templates.deliverabilityScore", handler)
}

func (h *VeridianDeliverabilityScoreHandler) handleScore(w http.ResponseWriter, r *http.Request) {
	req, err := h.parseRequest(r)
	if err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := h.service.Score(r.Context(), req)
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
		h.logger.WithField("error", err.Error()).Error("Failed to score template deliverability")
		WriteJSONError(w, "Failed to score template deliverability", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// parseRequest lit la requête depuis le body JSON (POST) ou la query (GET).
func (h *VeridianDeliverabilityScoreHandler) parseRequest(r *http.Request) (*domain.VeridianDeliverabilityScoreRequest, error) {
	req := &domain.VeridianDeliverabilityScoreRequest{}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
		if err != nil {
			return nil, errors.New("failed to read request body")
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, req); err != nil {
				return nil, errors.New("invalid JSON body")
			}
		}
	}

	// La query string surcharge / complète (utile pour GET, et tolérant pour un
	// POST sans body).
	q := r.URL.Query()
	if v := q.Get("workspace_id"); v != "" {
		req.WorkspaceID = v
	}
	if v := q.Get("subject"); v != "" {
		req.Subject = v
	}
	if v := q.Get("body"); v != "" {
		req.Body = v
	}
	if v := q.Get("from_domain"); v != "" {
		req.FromDomain = v
	}
	if v := q.Get("provider_class"); v != "" {
		req.ProviderClass = v
	}
	if v := q.Get("mode"); v != "" {
		req.Mode = v
	}
	if v := q.Get("is_html"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			req.IsHTML = b
		}
	}

	if req.WorkspaceID == "" {
		return nil, errors.New("workspace_id is required")
	}
	return req, nil
}
