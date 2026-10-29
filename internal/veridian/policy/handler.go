package policy

import (
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"

	httpHandler "github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Handler sert les routes du module (JWT ou clé API du workspace ; les permissions sont
// vérifiées par le service) :
//
//	GET|POST /api/veridian/policy.get                 (workspace:read)
//	POST     /api/veridian/policy.set                 (workspace:write)
//	POST     /api/veridian/policy.reset               (workspace:write)
//	GET|POST /api/veridian/policy.priority.list       (broadcasts:read + automations:read)
//	POST     /api/veridian/policy.priority.set        (broadcasts|automations:write)
//	POST     /api/veridian/policy.priority.clear      (broadcasts|automations:write)
type Handler struct {
	svc          *Service
	getJWTSecret func() ([]byte, error)
	log          logger.Logger
}

// NewHandler crée le handler.
func NewHandler(svc *Service, getJWTSecret func() ([]byte, error), log logger.Logger) *Handler {
	return &Handler{svc: svc, getJWTSecret: getJWTSecret, log: log}
}

// RegisterRoutes monte les routes sur le mux.
func (h *Handler) RegisterRoutes(mux *nethttp.ServeMux) {
	auth := middleware.NewAuthMiddleware(h.getJWTSecret).RequireAuth()
	both := func(path string, fn nethttp.HandlerFunc) {
		hd := auth(fn)
		mux.Handle("GET "+path, hd)
		mux.Handle("POST "+path, hd)
	}
	post := func(path string, fn nethttp.HandlerFunc) { mux.Handle("POST "+path, auth(fn)) }
	both("/api/veridian/policy.get", h.get)
	post("/api/veridian/policy.set", h.set)
	post("/api/veridian/policy.reset", h.reset)
	both("/api/veridian/policy.priority.list", h.priorityList)
	post("/api/veridian/policy.priority.set", h.prioritySet)
	post("/api/veridian/policy.priority.clear", h.priorityClear)
}

// Request lit les paramètres d'une route : query string, complétée par le corps JSON d'un POST.
type Request struct {
	query map[string]string
	body  map[string]json.RawMessage
}

// ReadRequest lit la requête (corps limité à 1 Mo). Une erreur est déjà écrite dans w.
func ReadRequest(w nethttp.ResponseWriter, r *nethttp.Request) (*Request, bool) {
	req := &Request{query: map[string]string{}, body: map[string]json.RawMessage{}}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			req.query[k] = v[0]
		}
	}
	if r.Method == nethttp.MethodPost {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || (len(strings.TrimSpace(string(raw))) > 0 && json.Unmarshal(raw, &req.body) != nil) {
			httpHandler.WriteJSONError(w, "corps JSON invalide", nethttp.StatusBadRequest)
			return nil, false
		}
	}
	return req, true
}

// Str : valeur texte (corps puis query).
func (q *Request) Str(key string) string {
	if raw, ok := q.body[key]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return strings.Trim(string(raw), `"`)
	}
	return q.query[key]
}

// Int : valeur entière ; ok=false si absente. err si présente mais illisible.
func (q *Request) Int(key string) (int, bool, error) {
	s := strings.TrimSpace(q.Str(key))
	if s == "" {
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f != float64(int(f)) {
		return 0, true, invalid("%s doit être un entier (reçu %q)", key, s)
	}
	return int(f), true, nil
}

// Float : valeur décimale ; ok=false si absente.
func (q *Request) Float(key string) (float64, bool, error) {
	s := strings.TrimSpace(strings.Replace(q.Str(key), ",", ".", 1))
	if s == "" {
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, true, invalid("%s doit être un nombre (reçu %q)", key, s)
	}
	return f, true, nil
}

// Strings : liste de textes (tableau JSON, ou texte séparé par des virgules).
func (q *Request) Strings(key string) []string {
	if raw, ok := q.body[key]; ok {
		var l []string
		if json.Unmarshal(raw, &l) == nil {
			return l
		}
	}
	var out []string
	for _, p := range strings.Split(q.Str(key), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// WriteError traduit une erreur de service en réponse HTTP.
func WriteError(w nethttp.ResponseWriter, log logger.Logger, err error, what string) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpHandler.WriteJSONError(w, ve.Msg, nethttp.StatusBadRequest)
	case errors.Is(err, ErrNotFound):
		httpHandler.WriteJSONError(w, err.Error(), nethttp.StatusNotFound)
	default:
		log.WithField("error", err.Error()).Error(what + ": échec")
		httpHandler.WriteAuthAwareError(w, err, what+": échec", nethttp.StatusInternalServerError)
	}
}

// WriteJSON écrit une réponse 200 JSON.
func WriteJSON(w nethttp.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(nethttp.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) get(w nethttp.ResponseWriter, r *nethttp.Request) {
	q, ok := ReadRequest(w, r)
	if !ok {
		return
	}
	v, err := h.svc.Get(r.Context(), q.Str("workspace_id"), q.Str("class"))
	if err != nil {
		WriteError(w, h.log, err, "policy.get")
		return
	}
	WriteJSON(w, v)
}

func (h *Handler) set(w nethttp.ResponseWriter, r *nethttp.Request) {
	q, ok := ReadRequest(w, r)
	if !ok {
		return
	}
	req := SetRequest{WorkspaceID: q.Str("workspace_id"), Class: q.Str("class"), Reason: q.Str("reason")}
	var err error
	if req.DailyCap, _, err = q.Int("daily_cap"); err == nil {
		if req.RatePerMin, _, err = q.Float("rate_per_min"); err == nil {
			if req.WarmupShare, _, err = q.Float("warmup_share"); err == nil {
				req.SlowdownThreshold, _, err = q.Float("slowdown_threshold")
			}
		}
	}
	if err != nil {
		WriteError(w, h.log, err, "policy.set")
		return
	}
	res, err := h.svc.Set(r.Context(), req)
	if err != nil {
		WriteError(w, h.log, err, "policy.set")
		return
	}
	WriteJSON(w, res)
}

func (h *Handler) reset(w nethttp.ResponseWriter, r *nethttp.Request) {
	q, ok := ReadRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Reset(r.Context(), q.Str("workspace_id"), q.Str("class"), q.Strings("rules"))
	if err != nil {
		WriteError(w, h.log, err, "policy.reset")
		return
	}
	WriteJSON(w, res)
}

func (h *Handler) priorityList(w nethttp.ResponseWriter, r *nethttp.Request) {
	q, ok := ReadRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.ListPriorities(r.Context(), q.Str("workspace_id"))
	if err != nil {
		WriteError(w, h.log, err, "policy.priority.list")
		return
	}
	WriteJSON(w, res)
}

func (h *Handler) prioritySet(w nethttp.ResponseWriter, r *nethttp.Request) {
	q, ok := ReadRequest(w, r)
	if !ok {
		return
	}
	p, present, err := q.Int("priority")
	if err == nil && !present {
		err = invalid("priority est requis (2 à 9)")
	}
	if err != nil {
		WriteError(w, h.log, err, "policy.priority.set")
		return
	}
	res, err := h.svc.SetPriority(r.Context(), q.Str("workspace_id"), q.Str("source_type"), q.Str("source_id"), p)
	if err != nil {
		WriteError(w, h.log, err, "policy.priority.set")
		return
	}
	WriteJSON(w, res)
}

func (h *Handler) priorityClear(w nethttp.ResponseWriter, r *nethttp.Request) {
	q, ok := ReadRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.ClearPriority(r.Context(), q.Str("workspace_id"), q.Str("source_type"), q.Str("source_id"))
	if err != nil {
		WriteError(w, h.log, err, "policy.priority.clear")
		return
	}
	WriteJSON(w, res)
}
