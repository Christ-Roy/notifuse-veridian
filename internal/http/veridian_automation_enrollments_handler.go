package http

// === Veridian — lecture des inscriptions d'automation (2026-10-27) ===
//
//	GET /api/automations.enrollments
//	  ?workspace_id=…&automation_id=…[&status=active|completed|exited|failed][&cursor=…][&limit=50]
//	  -> {"enrollments":[…],"next_cursor":"…"|""}
//
// Lecture seule, auth JWT/clé API du workspace (RequireAuth) et permission
// automations:read contrôlée par le service. Un paramètre invalide = 400 explicite.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianAutomationEnrollmentsListHandler sert GET /api/automations.enrollments.
type VeridianAutomationEnrollmentsListHandler struct {
	service      domain.VeridianAutomationEnrollmentService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianAutomationEnrollmentsListHandler construit le handler.
func NewVeridianAutomationEnrollmentsListHandler(svc domain.VeridianAutomationEnrollmentService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianAutomationEnrollmentsListHandler {
	return &VeridianAutomationEnrollmentsListHandler{service: svc, getJWTSecret: getJWTSecret, logger: log}
}

// RegisterRoutes branche la route GET sous auth.
func (h *VeridianAutomationEnrollmentsListHandler) RegisterRoutes(mux *http.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	mux.Handle("GET /api/automations.enrollments", requireAuth(http.HandlerFunc(h.handleList)))
}

func (h *VeridianAutomationEnrollmentsListHandler) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.VeridianAutomationEnrollmentFilter{
		WorkspaceID:  strings.TrimSpace(q.Get("workspace_id")),
		AutomationID: strings.TrimSpace(q.Get("automation_id")),
		Status:       strings.TrimSpace(q.Get("status")),
		Cursor:       strings.TrimSpace(q.Get("cursor")),
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			WriteJSONError(w, "invalid limit", http.StatusBadRequest)
			return
		}
		f.Limit = n
	}
	if err := f.Validate(); err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	page, err := h.service.ListEnrollments(r.Context(), f)
	if err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to list automation enrollments")
		WriteAuthAwareError(w, err, "Failed to list automation enrollments", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
