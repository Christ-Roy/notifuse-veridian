package reconcile

import (
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strings"

	httpx "github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Handler : routes /api/veridian/reconcile.{run,report,resolve}. Droits vérifiés par le service.
type Handler struct {
	Svc         *Service
	RequireAuth func(nethttp.Handler) nethttp.Handler
	Log         logger.Logger
}

// RegisterRoutes pose les trois routes (GET et POST pour run et report, POST pour resolve).
func (h *Handler) RegisterRoutes(mux *nethttp.ServeMux) {
	mux.Handle("GET /api/veridian/reconcile.report", h.RequireAuth(nethttp.HandlerFunc(h.report)))
	mux.Handle("POST /api/veridian/reconcile.report", h.RequireAuth(nethttp.HandlerFunc(h.report)))
	mux.Handle("POST /api/veridian/reconcile.run", h.RequireAuth(nethttp.HandlerFunc(h.run)))
	mux.Handle("POST /api/veridian/reconcile.resolve", h.RequireAuth(nethttp.HandlerFunc(h.resolve)))
}

type body struct {
	WorkspaceID     string   `json:"workspace_id"`
	Checks          []string `json:"checks"`
	Check           string   `json:"check"`
	IncludeResolved bool     `json:"include_resolved"`
	FindingID       string   `json:"finding_id"`
	Action          string   `json:"action"`
	Note            string   `json:"note"`
}

func readBody(r *nethttp.Request) (body, error) {
	var b body
	if r.Method == nethttp.MethodPost {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return b, errors.New("failed to read request body")
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &b); err != nil {
				return b, errors.New("invalid JSON body")
			}
		}
	}
	q := r.URL.Query()
	if v := q.Get("workspace_id"); v != "" {
		b.WorkspaceID = v
	}
	if v := q.Get("check"); v != "" {
		b.Check = v
	}
	if v := q.Get("checks"); v != "" {
		b.Checks = strings.Split(v, ",")
	}
	if q.Get("include_resolved") == "true" {
		b.IncludeResolved = true
	}
	return b, nil
}

func write(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) fail(w nethttp.ResponseWriter, err error, what string) {
	var bad *ErrBadRequest
	switch {
	case errors.As(err, &bad):
		httpx.WriteJSONError(w, bad.Msg, nethttp.StatusBadRequest)
	case errors.Is(err, ErrBusy):
		httpx.WriteJSONError(w, err.Error(), nethttp.StatusConflict)
	default:
		h.Log.WithField("error", err.Error()).Error("reconcile: " + what)
		httpx.WriteAuthAwareError(w, err, "Failed to "+what, nethttp.StatusInternalServerError)
	}
}

func (h *Handler) run(w nethttp.ResponseWriter, r *nethttp.Request) {
	b, err := readBody(r)
	if err != nil {
		httpx.WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	checks := b.Checks
	if b.Check != "" {
		checks = append(checks, b.Check)
	}
	rep, err := h.Svc.Run(r.Context(), b.WorkspaceID, checks)
	if err != nil {
		h.fail(w, err, "run reconcile")
		return
	}
	write(w, nethttp.StatusOK, rep)
}

func (h *Handler) report(w nethttp.ResponseWriter, r *nethttp.Request) {
	b, err := readBody(r)
	if err != nil {
		httpx.WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	rep, err := h.Svc.Report(r.Context(), b.WorkspaceID, b.Check, b.IncludeResolved)
	if err != nil {
		h.fail(w, err, "read reconcile report")
		return
	}
	write(w, nethttp.StatusOK, rep)
}

func (h *Handler) resolve(w nethttp.ResponseWriter, r *nethttp.Request) {
	b, err := readBody(r)
	if err != nil {
		httpx.WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
		return
	}
	res, err := h.Svc.Resolve(r.Context(), ResolveRequest{WorkspaceID: b.WorkspaceID, FindingID: b.FindingID, Action: b.Action, Note: b.Note})
	if err != nil {
		h.fail(w, err, "resolve reconcile finding")
		return
	}
	write(w, nethttp.StatusOK, res)
}
