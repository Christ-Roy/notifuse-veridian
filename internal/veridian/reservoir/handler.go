package reservoir

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	httpHandler "github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// maxBody : 1000 contacts enrichis tiennent largement.
const maxBody = 4 << 20

// Handler : routes du module.
type Handler struct {
	Svc         *Service
	RequireAuth func(http.Handler) http.Handler
	Log         logger.Logger
}

// RegisterRoutes branche les routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/veridian/contacts.intake", h.RequireAuth(http.HandlerFunc(h.intake)))
	mux.Handle("GET /api/veridian/reservoir.summary", h.RequireAuth(http.HandlerFunc(h.summary)))
	mux.Handle("POST /api/veridian/reservoir.activate", h.RequireAuth(http.HandlerFunc(h.activate)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	var te *TriggerError
	switch {
	case errors.As(err, &te):
		writeJSON(w, http.StatusConflict, map[string]any{"error": te.Error(), "code": "would_trigger", "triggers": te.Triggers})
	case errors.Is(err, ErrInvalid):
		httpHandler.WriteJSONError(w, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "), http.StatusBadRequest)
	default:
		h.Log.WithField("error", err.Error()).Error("reservoir: " + what)
		httpHandler.WriteAuthAwareError(w, err, what+" : échec", http.StatusInternalServerError)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		httpHandler.WriteJSONError(w, "corps trop volumineux", http.StatusRequestEntityTooLarge)
		return false
	}
	if err := json.Unmarshal(raw, v); err != nil {
		httpHandler.WriteJSONError(w, "corps JSON invalide : "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (h *Handler) intake(w http.ResponseWriter, r *http.Request) {
	var req IntakeRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.Svc.Intake(r.Context(), req)
	if err != nil {
		h.fail(w, err, "import groupé")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.Svc.Summary(r.Context(), q.Get("workspace_id"), strings.Split(q.Get("list_ids"), ","), q.Get("automation_id"))
	if err != nil {
		h.fail(w, err, "état des réservoirs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reservoirs": res})
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	var req ActivateRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.Svc.Activate(r.Context(), req)
	if err != nil {
		h.fail(w, err, "activation")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
