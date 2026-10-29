package api

import (
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"

	httpHandler "github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/veridian/folders"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Handler expose les routes /api/veridian/folders.* et /api/veridian/{segments,lists,templates}.move.
type Handler struct {
	Svc          *folders.Service
	GetJWTSecret func() ([]byte, error)
	Log          logger.Logger
}

// Routes : source unique des chemins, relue par les tests d'OpenAPI et de CLI.
var Routes = []string{
	"GET /api/veridian/folders.list", "POST /api/veridian/folders.list",
	"POST /api/veridian/folders.create", "POST /api/veridian/folders.rename",
	"POST /api/veridian/folders.move", "POST /api/veridian/folders.delete",
	"POST /api/veridian/folders.reorder",
	"POST /api/veridian/segments.move", "POST /api/veridian/lists.move", "POST /api/veridian/templates.move",
}

// RegisterRoutes monte les routes sur le mux, derriere l'authentification JWT / cle API.
func (h *Handler) RegisterRoutes(mux *nethttp.ServeMux) {
	auth := middleware.NewAuthMiddleware(h.GetJWTSecret).RequireAuth()
	mux.Handle("GET /api/veridian/folders.list", auth(nethttp.HandlerFunc(h.list)))
	mux.Handle("POST /api/veridian/folders.list", auth(nethttp.HandlerFunc(h.list)))
	mux.Handle("POST /api/veridian/folders.create", auth(nethttp.HandlerFunc(h.create)))
	mux.Handle("POST /api/veridian/folders.rename", auth(nethttp.HandlerFunc(h.rename)))
	mux.Handle("POST /api/veridian/folders.move", auth(nethttp.HandlerFunc(h.moveFolder)))
	mux.Handle("POST /api/veridian/folders.delete", auth(nethttp.HandlerFunc(h.deleteFolder)))
	mux.Handle("POST /api/veridian/folders.reorder", auth(nethttp.HandlerFunc(h.reorder)))
	for _, k := range folders.Kinds {
		kind := k
		mux.Handle("POST /api/veridian/"+string(kind)+".move", auth(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			h.moveItems(w, r, kind)
		})))
	}
}

const maxBody = 1 << 20

func decode(r *nethttp.Request, into any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func writeJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) fail(w nethttp.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, folders.ErrNotFound):
		httpHandler.WriteJSONError(w, err.Error(), nethttp.StatusNotFound)
	case errors.Is(err, folders.ErrConflict):
		httpHandler.WriteJSONError(w, err.Error(), nethttp.StatusConflict)
	case errors.Is(err, folders.ErrInvalid):
		httpHandler.WriteJSONError(w, err.Error(), nethttp.StatusBadRequest)
	default:
		if h.Log != nil {
			h.Log.WithField("error", err.Error()).Error("folders: " + what)
		}
		httpHandler.WriteAuthAwareError(w, err, "Failed to "+what, nethttp.StatusInternalServerError)
	}
}

func posOf(index *int, before string) folders.Position {
	return folders.Position{Index: index, Before: before}
}

func (h *Handler) list(w nethttp.ResponseWriter, r *nethttp.Request) {
	q := r.URL.Query()
	req := struct {
		WorkspaceID string `json:"workspace_id"`
		Kind        string `json:"kind"`
	}{q.Get("workspace_id"), q.Get("kind")}
	if r.Method == nethttp.MethodPost {
		if err := decode(r, &req); err != nil {
			httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
			return
		}
		if req.WorkspaceID == "" {
			req.WorkspaceID = q.Get("workspace_id")
		}
		if req.Kind == "" {
			req.Kind = q.Get("kind")
		}
	}
	v, err := h.Svc.View(r.Context(), req.WorkspaceID, folders.Kind(req.Kind))
	if err != nil {
		h.fail(w, err, "list folders")
		return
	}
	writeJSON(w, nethttp.StatusOK, v)
}

func (h *Handler) create(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		Path        string `json:"path"` // alternative a name+parent_id : cree tous les niveaux manquants
		ParentID    string `json:"parent_id"`
		Position    *int   `json:"position"`
		BeforeID    string `json:"before_id"`
	}
	if err := decode(r, &req); err != nil {
		httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
		return
	}
	var res *folders.Result
	var err error
	if req.Path != "" {
		if req.Name != "" || req.ParentID != "" {
			httpHandler.WriteJSONError(w, "path exclut name et parent_id", nethttp.StatusBadRequest)
			return
		}
		res, err = h.Svc.EnsurePath(r.Context(), req.WorkspaceID, folders.Kind(req.Kind), req.Path)
	} else {
		res, err = h.Svc.CreateFolder(r.Context(), req.WorkspaceID, folders.Kind(req.Kind), req.Name, req.ParentID, posOf(req.Position, req.BeforeID))
	}
	if err != nil {
		h.fail(w, err, "create folder")
		return
	}
	writeJSON(w, nethttp.StatusCreated, res)
}

func (h *Handler) rename(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		ID          string `json:"id"`
		Name        string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
		return
	}
	res, err := h.Svc.RenameFolder(r.Context(), req.WorkspaceID, req.ID, req.Name)
	if err != nil {
		h.fail(w, err, "rename folder")
		return
	}
	writeJSON(w, nethttp.StatusOK, res)
}

func (h *Handler) moveFolder(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		ID          string `json:"id"`
		ParentID    string `json:"parent_id"`
		Position    *int   `json:"position"`
		BeforeID    string `json:"before_id"`
	}
	if err := decode(r, &req); err != nil {
		httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
		return
	}
	res, err := h.Svc.MoveFolder(r.Context(), req.WorkspaceID, req.ID, req.ParentID, posOf(req.Position, req.BeforeID))
	if err != nil {
		h.fail(w, err, "move folder")
		return
	}
	writeJSON(w, nethttp.StatusOK, res)
}

func (h *Handler) deleteFolder(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		WorkspaceID string `json:"workspace_id"`
		ID          string `json:"id"`
		Contents    string `json:"contents"` // "" ou "refuse" : refuser si non vide ; "to_parent" : remonter le contenu
	}
	if err := decode(r, &req); err != nil {
		httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
		return
	}
	if req.Contents != "" && req.Contents != "refuse" && req.Contents != "to_parent" {
		httpHandler.WriteJSONError(w, "contents doit valoir refuse ou to_parent", nethttp.StatusBadRequest)
		return
	}
	res, err := h.Svc.DeleteFolder(r.Context(), req.WorkspaceID, req.ID, req.Contents == "to_parent")
	if err != nil {
		h.fail(w, err, "delete folder")
		return
	}
	writeJSON(w, nethttp.StatusOK, res)
}

func (h *Handler) reorder(w nethttp.ResponseWriter, r *nethttp.Request) {
	var req struct {
		WorkspaceID string   `json:"workspace_id"`
		Kind        string   `json:"kind"`
		ParentID    string   `json:"parent_id"`
		FolderIDs   []string `json:"folder_ids"`
		ItemIDs     []string `json:"item_ids"`
	}
	if err := decode(r, &req); err != nil {
		httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
		return
	}
	res, err := h.Svc.Reorder(r.Context(), req.WorkspaceID, folders.Kind(req.Kind), req.ParentID, req.FolderIDs, req.ItemIDs)
	if err != nil {
		h.fail(w, err, "reorder folders")
		return
	}
	writeJSON(w, nethttp.StatusOK, res)
}

func (h *Handler) moveItems(w nethttp.ResponseWriter, r *nethttp.Request, kind folders.Kind) {
	var req struct {
		WorkspaceID string   `json:"workspace_id"`
		ID          string   `json:"id"`
		IDs         []string `json:"ids"`
		FolderID    string   `json:"folder_id"`   // "" = racine
		FolderPath  string   `json:"folder_path"` // alternative a folder_id : `A / B` (cree les niveaux manquants)
		Position    *int     `json:"position"`
		BeforeID    string   `json:"before_id"`
		Name        string   `json:"name"` // nouveau libelle court (un seul objet)
	}
	if err := decode(r, &req); err != nil {
		httpHandler.WriteJSONError(w, "Invalid request body", nethttp.StatusBadRequest)
		return
	}
	ids := req.IDs
	if req.ID != "" {
		ids = append([]string{req.ID}, ids...)
	}
	if req.FolderID != "" && req.FolderPath != "" {
		httpHandler.WriteJSONError(w, "folder_id et folder_path s'excluent", nethttp.StatusBadRequest)
		return
	}
	var res *folders.Result
	var err error
	if req.FolderPath != "" {
		res, err = h.Svc.MoveItemsToPath(r.Context(), req.WorkspaceID, kind, ids, req.FolderPath, posOf(req.Position, req.BeforeID), req.Name)
	} else {
		res, err = h.Svc.MoveItems(r.Context(), req.WorkspaceID, kind, ids, req.FolderID, posOf(req.Position, req.BeforeID), req.Name)
	}
	if err != nil {
		h.fail(w, err, "move "+string(kind))
		return
	}
	writeJSON(w, nethttp.StatusOK, res)
}
