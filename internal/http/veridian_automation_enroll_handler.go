package http

// === Veridian patch — sprint AI-first API (2026-06-15) ===
// Handler d'enrôlement programmatique de contacts dans une automation.
//
// Route POST /api/automations.enroll — auth JWT console (RequireAuth),
// gardien d'appartenance workspace + permission automations:write dans le
// service. C'est le maillon manquant du pilotage "AI-first" : créer/activer une
// séquence était déjà possible par API, y enrôler un contact ne l'était pas.
//
// ⚠️ POST ET GET routés explicitement (Go 1.22+ exige la méthode dans le
// pattern). C'est une MUTATION : GET renvoie 405. Sans route GET explicite, un
// GET tomberait dans le catchall SPA de root_handler.go (HTML 200 trompeur, zéro
// log) — piège P0 vécu 2026-05-25, cf. CLAUDE.md "Pièges historiques".
//
// Payload : {"workspace_id":"...","automation_id":"...","contact_emails":["a@x","b@y"]}
// Réponse : {"enrolled":N,"skipped":N,"failed":N,"results":[{"email":"...","status":"enrolled|already_active|error"}]}

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianAutomationEnrollHandler exposes POST /api/automations.enroll.
type VeridianAutomationEnrollHandler struct {
	service      *service.VeridianAutomationEnrollService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianAutomationEnrollHandler builds the handler.
func NewVeridianAutomationEnrollHandler(
	svc *service.VeridianAutomationEnrollService,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) *VeridianAutomationEnrollHandler {
	return &VeridianAutomationEnrollHandler{
		service:      svc,
		getJWTSecret: getJWTSecret,
		logger:       log,
	}
}

// RegisterRoutes registers POST (+ GET → 405) on /api/automations.enroll.
func (h *VeridianAutomationEnrollHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := middleware.NewAuthMiddleware(h.getJWTSecret)
	requireAuth := authMiddleware.RequireAuth()
	handler := requireAuth(http.HandlerFunc(h.handleEnroll))
	mux.Handle("POST /api/automations.enroll", handler)
	// Route GET explicitement pour ne PAS tomber dans le catchall SPA : on
	// répond 405 (mutation only) au lieu de servir du HTML 200 trompeur.
	mux.Handle("GET /api/automations.enroll", handler)
}

func (h *VeridianAutomationEnrollHandler) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		WriteJSONError(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req domain.VeridianEnrollContactsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	emails, err := req.Validate()
	if err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := h.service.Enroll(r.Context(), req.WorkspaceID, req.AutomationID, emails)
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
		h.logger.WithField("error", err.Error()).Error("Failed to enroll contacts into automation")
		WriteJSONError(w, "Failed to enroll contacts", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
