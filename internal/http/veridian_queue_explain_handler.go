package http

import (
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianQueueExplainHandler sert, pour l'explorateur de file (fiche 62, lot 1) :
//
//	GET|POST /api/veridian/queue.explain    (automations:read)
//	GET|POST /api/veridian/decisions.list   (automations:read)
//	POST     /api/veridian/queue.recompute  (automations:write, borne, jamais tout le workspace)
//
// Les permissions sont verifiees par le service.
type VeridianQueueExplainHandler struct {
	service      domain.VeridianQueueExplainService
	getJWTSecret func() ([]byte, error)
	logger       logger.Logger
}

func NewVeridianQueueExplainHandler(svc domain.VeridianQueueExplainService, getJWTSecret func() ([]byte, error), log logger.Logger) *VeridianQueueExplainHandler {
	return &VeridianQueueExplainHandler{service: svc, getJWTSecret: getJWTSecret, logger: log}
}

func (h *VeridianQueueExplainHandler) RegisterRoutes(mux *nethttp.ServeMux) {
	requireAuth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	explain := requireAuth(nethttp.HandlerFunc(h.handleExplain))
	mux.Handle("GET /api/veridian/queue.explain", explain)
	mux.Handle("POST /api/veridian/queue.explain", explain)
	decisions := requireAuth(nethttp.HandlerFunc(h.handleDecisions))
	mux.Handle("GET /api/veridian/decisions.list", decisions)
	mux.Handle("POST /api/veridian/decisions.list", decisions)
	mux.Handle("POST /api/veridian/queue.recompute", requireAuth(nethttp.HandlerFunc(h.handleRecompute)))
}

// veridianQueueParams fusionne le corps JSON (POST) et la query string (elle l'emporte).
func veridianQueueParams(r *nethttp.Request) (map[string]string, error) {
	params := map[string]string{}
	if r.Method == nethttp.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return nil, errors.New("failed to read request body")
		}
		if len(body) > 0 {
			raw := map[string]interface{}{}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, errors.New("invalid JSON body")
			}
			for k, v := range raw {
				switch t := v.(type) {
				case string:
					params[k] = t
				case float64:
					params[k] = strconv.FormatFloat(t, 'f', -1, 64)
				case bool:
					params[k] = strconv.FormatBool(t)
				case []interface{}:
					parts := make([]string, 0, len(t))
					for _, e := range t {
						if s, ok := e.(string); ok {
							parts = append(parts, s)
						}
					}
					params[k] = strings.Join(parts, ",")
				}
			}
		}
	}
	for k, v := range r.URL.Query() {
		if len(v) > 0 && v[0] != "" {
			params[k] = v[0]
		}
	}
	return params, nil
}

func (h *VeridianQueueExplainHandler) fail(w nethttp.ResponseWriter, err error, msg string) {
	if errors.Is(err, service.ErrVeridianQueueEntryNotFound) {
		WriteJSONError(w, "queue entry not found", nethttp.StatusNotFound)
		return
	}
	var invalid domain.ErrVeridianQueueRecompute
	if errors.As(err, &invalid) {
		WriteJSONError(w, invalid.Error(), nethttp.StatusBadRequest)
		return
	}
	if strings.HasPrefix(err.Error(), "invalid ") {
		WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	h.logger.WithField("error", err.Error()).Error(msg)
	WriteAuthAwareError(w, err, msg, nethttp.StatusInternalServerError)
}

func (h *VeridianQueueExplainHandler) handleExplain(w nethttp.ResponseWriter, r *nethttp.Request) {
	p, err := veridianQueueParams(r)
	if err != nil {
		WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	if p["workspace_id"] == "" {
		WriteJSONError(w, "workspace_id is required", nethttp.StatusBadRequest)
		return
	}
	f := domain.VeridianQueueExplainFilter{
		AutomationID: p["automation_id"], NodeID: p["node_id"], Reason: p["reason"],
		ProfileID: p["profile_id"], Class: p["class"], Status: p["status"], EntryID: p["entry_id"],
	}
	for _, part := range strings.Split(p["group_by"], ",") {
		if part = strings.TrimSpace(part); part != "" {
			f.GroupBy = append(f.GroupBy, part)
		}
	}
	out, err := h.service.Explain(r.Context(), p["workspace_id"], f)
	if err != nil {
		h.fail(w, err, "Failed to explain queue")
		return
	}
	writeJSON(w, nethttp.StatusOK, out)
}

// veridianParseSince accepte une duree (30m, 2h, 7d) ou une date RFC3339.
func veridianParseSince(v string, now time.Time) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, nil
	}
	if strings.HasSuffix(v, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && n > 0 {
			t := now.Add(-time.Duration(n) * 24 * time.Hour)
			return &t, nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		t := now.Add(-d)
		return &t, nil
	}
	return nil, errors.New("invalid since (use 30m, 2h, 7d or an RFC3339 date)")
}

func (h *VeridianQueueExplainHandler) handleDecisions(w nethttp.ResponseWriter, r *nethttp.Request) {
	p, err := veridianQueueParams(r)
	if err != nil {
		WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	if p["workspace_id"] == "" {
		WriteJSONError(w, "workspace_id is required", nethttp.StatusBadRequest)
		return
	}
	since, err := veridianParseSince(p["since"], time.Now())
	if err != nil {
		WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	limit := 0
	if v := p["limit"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			WriteJSONError(w, "invalid limit", nethttp.StatusBadRequest)
			return
		}
		limit = n
	}
	f := domain.VeridianSendDecisionFilter{
		AutomationID: p["automation_id"], NodeID: p["node_id"], Email: p["email"], EntryID: p["entry_id"],
		Reason: p["reason"], Outcome: p["outcome"], Since: since, Limit: limit, Cursor: p["cursor"],
		WithTrace: p["trace"] == "1" || p["trace"] == "true",
	}
	list, next, level, err := h.service.Decisions(r.Context(), p["workspace_id"], f)
	if err != nil {
		h.fail(w, err, "Failed to list send decisions")
		return
	}
	if list == nil {
		list = []*domain.VeridianSendDecision{}
	}
	writeJSON(w, nethttp.StatusOK, map[string]interface{}{"decisions": list, "next_cursor": next, "level": level})
}

func (h *VeridianQueueExplainHandler) handleRecompute(w nethttp.ResponseWriter, r *nethttp.Request) {
	p, err := veridianQueueParams(r)
	if err != nil {
		WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	req := domain.VeridianQueueRecomputeRequest{
		WorkspaceID: p["workspace_id"], AutomationID: p["automation_id"], NodeID: p["node_id"],
		Reason: p["reason"], ProfileID: p["profile_id"],
	}
	if v := p["limit"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			WriteJSONError(w, "invalid limit", nethttp.StatusBadRequest)
			return
		}
		req.Limit = n
	}
	for _, id := range strings.Split(p["entry_ids"], ",") {
		if id = strings.TrimSpace(id); id != "" {
			req.EntryIDs = append(req.EntryIDs, id)
		}
	}
	n, err := h.service.Recompute(r.Context(), req)
	if err != nil {
		h.fail(w, err, "Failed to recompute queue")
		return
	}
	writeJSON(w, nethttp.StatusOK, map[string]int{"recomputed": n})
}
