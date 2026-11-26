package domauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// DomainStore : ce que l'API demande au dépôt.
type DomainStore interface {
	Create(ctx context.Context, ws, domain, createdBy string) (*Domain, error)
	List(ctx context.Context, ws string) ([]Domain, error)
	Get(ctx context.Context, ws, id string) (*Domain, error)
	SaveCheck(ctx context.Context, ws, id, status string, checks []Check) (*Domain, error)
	Delete(ctx context.Context, ws, id string) (*Domain, error)
}

// DNSChecker : la lecture DNS (Verifier en production).
type DNSChecker interface {
	Verify(ctx context.Context, d Domain) []Check
}

// Handler : routes /api/veridian/sendingDomains.*
type Handler struct {
	Store       DomainStore
	DNS         DNSChecker
	Auth        mailapi.Authorizer
	Include     string
	Log         logger.Logger
	RequireAuth func(http.Handler) http.Handler
	// Changed : appelé après création, vérification ou suppression (vide le cache du signataire).
	Changed func(ws, domain string)
}

// View : un domaine tel que l'API le rend, avec ses enregistrements à publier. Jamais la clé privée.
type View struct {
	Domain
	Complete bool     `json:"complete"`
	Records  []Record `json:"records"`
}

// RegisterRoutes branche les routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/veridian/sendingDomains.list", h.RequireAuth(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/veridian/sendingDomains.create", h.RequireAuth(http.HandlerFunc(h.create)))
	mux.Handle("POST /api/veridian/sendingDomains.verify", h.RequireAuth(http.HandlerFunc(h.verify)))
	mux.Handle("POST /api/veridian/sendingDomains.delete", h.RequireAuth(http.HandlerFunc(h.delete)))
}

func (h *Handler) view(d Domain) View {
	return View{Domain: d, Complete: d.Complete(), Records: Records(d, h.Include)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code, "message": msg})
}

func (h *Handler) fail(w http.ResponseWriter, err error) {
	var pe *domain.PermissionError
	switch {
	case errors.As(err, &pe):
		writeErr(w, http.StatusForbidden, "forbidden", "droit insuffisant sur ce workspace")
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrExists), errors.Is(err, ErrTooMany):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrInvalidName):
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
	default:
		if h.Log != nil {
			h.Log.WithField("error", err.Error()).Error("Sending domains API failed")
		}
		writeErr(w, http.StatusInternalServerError, "internal", "erreur interne")
	}
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, ws string, perm domain.PermissionType) (context.Context, mailapi.Actor, bool) {
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "workspace_id requis")
		return nil, mailapi.Actor{}, false
	}
	ctx, actor, err := h.Auth.Authorize(r.Context(), ws, domain.PermissionResourceWorkspace, perm)
	if err != nil {
		var pe *domain.PermissionError
		if errors.As(err, &pe) {
			h.fail(w, err)
		} else {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "authentification refusée pour ce workspace")
		}
		return nil, mailapi.Actor{}, false
	}
	return ctx, actor, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "corps JSON invalide")
		return false
	}
	return true
}

func (h *Handler) changed(ws, d string) {
	if h.Changed != nil {
		h.Changed(ws, d)
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace_id")
	ctx, _, ok := h.authorize(w, r, ws, domain.PermissionTypeRead)
	if !ok {
		return
	}
	l, err := h.Store.List(ctx, ws)
	if err != nil {
		h.fail(w, err)
		return
	}
	out := make([]View, 0, len(l))
	for _, d := range l {
		out = append(out, h.view(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": out, "spf_include": h.Include})
}

type createRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Domain      string `json:"domain"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var q createRequest
	if !decode(w, r, &q) {
		return
	}
	ctx, actor, ok := h.authorize(w, r, q.WorkspaceID, domain.PermissionTypeWrite)
	if !ok {
		return
	}
	d, err := h.Store.Create(ctx, q.WorkspaceID, q.Domain, strings.TrimSpace(actor.Kind+":"+actor.ID))
	if err != nil {
		h.fail(w, err)
		return
	}
	h.changed(q.WorkspaceID, d.Domain)
	writeJSON(w, http.StatusCreated, map[string]any{"domain": h.view(*d)})
}

type idRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ID          string `json:"id"`
}

func (h *Handler) byID(w http.ResponseWriter, r *http.Request) (context.Context, idRequest, bool) {
	var q idRequest
	if !decode(w, r, &q) {
		return nil, q, false
	}
	ctx, _, ok := h.authorize(w, r, q.WorkspaceID, domain.PermissionTypeWrite)
	if !ok {
		return nil, q, false
	}
	if q.ID == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "id requis")
		return nil, q, false
	}
	return ctx, q, true
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	ctx, q, ok := h.byID(w, r)
	if !ok {
		return
	}
	d, err := h.Store.Get(ctx, q.WorkspaceID, q.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	checks := h.DNS.Verify(ctx, *d)
	d, err = h.Store.SaveCheck(ctx, q.WorkspaceID, q.ID, StatusOf(d.Status, checks), checks)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.changed(q.WorkspaceID, d.Domain)
	writeJSON(w, http.StatusOK, map[string]any{"domain": h.view(*d)})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	ctx, q, ok := h.byID(w, r)
	if !ok {
		return
	}
	d, err := h.Store.Delete(ctx, q.WorkspaceID, q.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.changed(q.WorkspaceID, d.Domain)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": d.ID, "domain": d.Domain})
}
