package hubusers

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
)

// Member : un humain membre d'un espace, vu par le Hub. Rien d'autre ne sort de cette route.
type Member struct {
	WorkspaceID string  `json:"workspace_id"`
	HubTenantID *string `json:"hub_tenant_id"` // null : espace que le Hub n'a pas encore relié (V73)
	HubUserID   *string `json:"hub_user_id"`   // null : compte non relié au Hub
	Email       string  `json:"email"`
	Role        string  `json:"role"`
}

// Les clés API ne sont pas des humains. Un espace supprimé (veridian_plan.deleted_at) ne sort pas. Un compte supprimé au Hub
// mais encore membre SORT : c'est l'écart que la réconciliation doit voir.
const listMembersSQL = `
	SELECT uw.workspace_id, p.hub_tenant_id, u.hub_user_id, u.email, uw.role
	FROM user_workspaces uw
	JOIN users u ON u.id = uw.user_id AND u.type = 'user'
	JOIN workspaces w ON w.id = uw.workspace_id
	LEFT JOIN veridian_plan p ON p.workspace_id = w.id
	WHERE p.deleted_at IS NULL AND ($1 = '' OR w.id = $1)
	ORDER BY uw.workspace_id, u.email`

const workspaceLiveSQL = `
	SELECT EXISTS (SELECT 1 FROM workspaces w LEFT JOIN veridian_plan p ON p.workspace_id = w.id
	WHERE w.id = $1 AND p.deleted_at IS NULL)`

func (s *SQLStore) ListMembers(ctx context.Context, workspaceID string) ([]Member, error) {
	rows, err := s.DB.QueryContext(ctx, listMembersSQL, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var tenant, hub sql.NullString
		if err := rows.Scan(&m.WorkspaceID, &tenant, &hub, &m.Email, &m.Role); err != nil {
			return nil, err
		}
		m.HubTenantID, m.HubUserID = strOrNil(tenant), strOrNil(hub)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *SQLStore) WorkspaceLive(ctx context.Context, workspaceID string) (bool, error) {
	var live bool
	err := s.DB.QueryRowContext(ctx, workspaceLiveSQL, workspaceID).Scan(&live)
	return live, err
}

func strOrNil(v sql.NullString) *string {
	if !v.Valid || v.String == "" {
		return nil
	}
	return &v.String
}

type membersResponse struct {
	Members []Member `json:"members"`
}

// Members : GET /api/veridian/membres[?workspace_id=]. Lecture seule, ne crée et ne modifie rien. Un espace inconnu ou supprimé
// répond 404 : une liste vide voudrait dire « cet espace n'a aucun humain ».
func (h *Handler) Members(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ws := strings.TrimSpace(q.Get("workspace_id"))
	if ws == "" && q.Has("workspace_id") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_payload", "hint": "workspace_id is empty"})
		return
	}
	if ws != "" {
		live, err := h.Store.WorkspaceLive(r.Context(), ws)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		if !live {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace_not_found"})
			return
		}
	}
	members, err := h.Store.ListMembers(r.Context(), ws)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, membersResponse{Members: members})
}
