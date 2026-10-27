package http

// === Veridian — file d'envoi : lister et annuler (2026-10-27) ===
//
//	GET  /api/veridian/queue.entries  ?workspace_id&automation_id[&node_id&profile_id&class&status&cursor&limit]
//	     -> {"entries":[…],"next_cursor":"…"}        (automations:read)
//	POST /api/veridian/queue.cancel   {"workspace_id","automation_id"|"entry_ids","reason","status?","limit?","dry_run?"}
//	     -> {"dry_run","matched","cancelled","entries":[…]}   (automations:write, motif obligatoire, journalisé)
//
// Une annulation n'a jamais lieu sans motif ni périmètre. Le journal est lisible
// par decisions.list (outcome « cancelled »).

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianQueueEntriesHandler sert la liste et l'annulation des entrées de file.
type VeridianQueueEntriesHandler struct {
	service      domain.VeridianQueueEntriesService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

// NewVeridianQueueEntriesHandler construit le handler.
func NewVeridianQueueEntriesHandler(svc domain.VeridianQueueEntriesService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianQueueEntriesHandler {
	return &VeridianQueueEntriesHandler{service: svc, getJWTSecret: getJWTSecret, logger: log}
}

// RegisterRoutes branche les deux routes sous auth.
func (h *VeridianQueueEntriesHandler) RegisterRoutes(mux *http.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	mux.Handle("GET /api/veridian/queue.entries", requireAuth(http.HandlerFunc(h.handleList)))
	mux.Handle("POST /api/veridian/queue.cancel", requireAuth(http.HandlerFunc(h.handleCancel)))
}

func (h *VeridianQueueEntriesHandler) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := domain.VeridianQueueEntriesFilter{
		WorkspaceID:  strings.TrimSpace(q.Get("workspace_id")),
		AutomationID: strings.TrimSpace(q.Get("automation_id")),
		NodeID:       strings.TrimSpace(q.Get("node_id")),
		ProfileID:    strings.TrimSpace(q.Get("profile_id")),
		Class:        strings.TrimSpace(q.Get("class")),
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
	page, err := h.service.ListEntries(r.Context(), f)
	if err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to list queue entries")
		WriteAuthAwareError(w, err, "Failed to list queue entries", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *VeridianQueueEntriesHandler) handleCancel(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		WriteJSONError(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	var req domain.VeridianQueueCancelRequest
	if err := json.Unmarshal(body, &req); err != nil {
		WriteJSONError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if err := req.Validate(); err != nil {
		WriteJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := h.service.CancelEntries(r.Context(), req)
	if err != nil {
		h.logger.WithField("error", err.Error()).Error("Failed to cancel queue entries")
		WriteAuthAwareError(w, err, "Failed to cancel queue entries", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
