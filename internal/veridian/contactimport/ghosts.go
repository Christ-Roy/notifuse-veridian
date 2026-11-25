package contactimport

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
)

// GhostRoute : combien de contacts sont inscrits à une liste qui n'existe pas, espace par espace (opérateur, HMAC Hub). Des nombres, jamais une
// adresse ; rien n'est modifié. Une liste supprimée garde ses inscriptions : elle n'est pas fantôme.
const GhostRoute = "GET /api/veridian/admin/ghost-lists"

// Workspaces énumère les espaces vivants et donne leur base.
type Workspaces interface {
	Conn
	List(ctx context.Context) ([]*domain.Workspace, error)
}

// Ghosts : le relevé d'un espace.
type Ghosts struct {
	WorkspaceID string `json:"workspace_id"`
	Lists       int    `json:"lists"`
	Contacts    int    `json:"contacts"`
}

// CountGhosts rend les espaces touchés, le nombre d'espaces lus et ceux qui n'ont pas pu l'être.
func CountGhosts(ctx context.Context, ws Workspaces) (hit []Ghosts, read int, unread []string, err error) {
	all, err := ws.List(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	hit, unread = []Ghosts{}, []string{}
	for _, w := range all {
		g := Ghosts{WorkspaceID: w.ID}
		db, err := ws.GetConnection(ctx, w.ID)
		if err == nil {
			err = db.QueryRowContext(ctx, `SELECT count(DISTINCT cl.list_id), count(*) FROM contact_lists cl
				WHERE cl.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM lists l WHERE l.id = cl.list_id)`).Scan(&g.Lists, &g.Contacts)
		}
		if err != nil {
			unread = append(unread, w.ID)
			continue
		}
		read++
		if g.Contacts > 0 {
			hit = append(hit, g)
		}
	}
	return hit, read, unread, nil
}

// MountGhosts pose le relevé sous HMAC Hub (503 sans secret).
func MountGhosts(mux *http.ServeMux, ws Workspaces, hubSecret string) {
	mux.Handle(GhostRoute, middleware.VeridianHMACMiddleware(hubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		hit, read, unread, err := CountGhosts(ctx, ws)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"relevé non abouti"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"workspaces_read": read, "workspaces_unread": unread, "ghosts": hit})
	})))
}
