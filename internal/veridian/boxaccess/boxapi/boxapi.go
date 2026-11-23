// Package boxapi expose les accès de boîte en HTTP : lister, créer (le mot de passe est rendu une seule fois), révoquer.
package boxapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Store : dépôt des accès.
type Store interface {
	Create(ctx context.Context, a boxaccess.Access) (*boxaccess.Access, string, error)
	List(ctx context.Context, ws string) ([]boxaccess.Access, error)
	Revoke(ctx context.Context, ws, id string) (*boxaccess.Access, error)
}

// Endpoint : réglage de connexion à donner au client mail. Port 0 : non servi sur cette instance.
type Endpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
}

// Connection : ce qu'un client mail doit saisir.
type Connection struct {
	IMAP *Endpoint `json:"imap,omitempty"`
	SMTP *Endpoint `json:"smtp,omitempty"`
}

// Handler : routes /api/veridian/mail.access.*
type Handler struct {
	Store       Store
	Auth        mailapi.Authorizer
	WS          boxaccess.WorkspaceGetter
	Connection  Connection
	Log         logger.Logger
	RequireAuth func(http.Handler) http.Handler
}

// RegisterRoutes branche les routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/veridian/mail.access.list", h.RequireAuth(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/veridian/mail.access.create", h.RequireAuth(http.HandlerFunc(h.create)))
	mux.Handle("POST /api/veridian/mail.access.revoke", h.RequireAuth(http.HandlerFunc(h.revoke)))
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
	case errors.Is(err, boxaccess.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, boxaccess.ErrTooMany):
		writeErr(w, http.StatusConflict, "too_many", err.Error())
	default:
		h.Log.WithField("error", err.Error()).Error("Box access API failed")
		writeErr(w, http.StatusInternalServerError, "internal", "erreur interne")
	}
}

// authorize : lire ou gérer les accès demande le droit « workspace ». Une authentification refusée répond 401.
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
	d := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "corps JSON invalide")
		return false
	}
	return true
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
	writeJSON(w, http.StatusOK, map[string]any{"accesses": l, "connection": h.Connection})
}

type createRequest struct {
	WorkspaceID string `json:"workspace_id"`
	ProfileID   string `json:"profile_id"`
	Address     string `json:"address"`
	Label       string `json:"label"`
	CanSend     *bool  `json:"can_send"`
	DailyCap    int    `json:"daily_cap"`
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
	q.Label = strings.TrimSpace(q.Label)
	if q.ProfileID == "" || len(q.Label) > 120 || q.DailyCap < 0 || q.DailyCap > boxaccess.MaxDailyCap {
		writeErr(w, http.StatusBadRequest, "invalid", "profile_id requis, libellé de 120 caractères au plus, plafond entre 1 et 1000")
		return
	}
	ws, err := h.WS.GetByID(ctx, q.WorkspaceID)
	if err != nil {
		h.fail(w, err)
		return
	}
	addr := q.Address
	if addr == "" {
		addr = defaultSender(ws, q.ProfileID)
	}
	if boxaccess.ProfileOf(ws, q.ProfileID, addr) == nil {
		writeErr(w, http.StatusBadRequest, "invalid", "profil d'envoi introuvable, ou adresse qui n'est pas une de ses adresses")
		return
	}
	send := q.CanSend == nil || *q.CanSend
	a, secret, err := h.Store.Create(ctx, boxaccess.Access{WorkspaceID: q.WorkspaceID, ProfileID: q.ProfileID, Address: addr,
		Label: q.Label, CanSend: send, DailyCap: q.DailyCap, CreatedBy: actor.Label()})
	if err != nil {
		h.fail(w, err)
		return
	}
	h.Log.WithFields(map[string]interface{}{"workspace_id": a.WorkspaceID, "access_id": a.ID, "profile_id": a.ProfileID, "by": actor.Label()}).Info("Box access created")
	writeJSON(w, http.StatusCreated, map[string]any{"access": a, "username": a.Address, "password": secret, "connection": h.Connection})
}

func defaultSender(ws *domain.Workspace, profileID string) string {
	p := ws.GetIntegrationByID(profileID)
	if p == nil {
		return ""
	}
	first := ""
	for _, s := range p.EmailProvider.Senders {
		if s.IsDefault {
			return s.Email
		}
		if first == "" {
			first = s.Email
		}
	}
	return first
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	var q struct {
		WorkspaceID string `json:"workspace_id"`
		ID          string `json:"id"`
	}
	if !decode(w, r, &q) {
		return
	}
	ctx, actor, ok := h.authorize(w, r, q.WorkspaceID, domain.PermissionTypeWrite)
	if !ok {
		return
	}
	a, err := h.Store.Revoke(ctx, q.WorkspaceID, q.ID)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.Log.WithFields(map[string]interface{}{"workspace_id": a.WorkspaceID, "access_id": a.ID, "by": actor.Label()}).Info("Box access revoked")
	writeJSON(w, http.StatusOK, map[string]any{"access": a})
}
