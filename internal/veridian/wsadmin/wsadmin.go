// Package wsadmin pose et relit le rôle « admin » d'un espace (VER-263, fiche 95), par deux routes signées du Hub :
//
//	POST /api/hub/workspace-admins                 {workspace_id,hub_user_id,action,actor_hub_user_id,reason,reinstate}
//	GET  /api/hub/workspace-admins?workspace_id=X   membres humains, rôle et désignation
//
// La personne est son hub_user_id, jamais une adresse. Rien n'est créé ni ajouté : elle doit déjà être membre. Un propriétaire n'est jamais touché :
// sa désignation est notée et ne prend effet que s'il cède la propriété. Une personne retirée ne revient que sur `reinstate` explicite. Chaque
// décision, refus compris, est journalisée.
package wsadmin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	actionGrant  = "grant"
	actionRevoke = "revoke"
	maxReason    = 500
)

// Request : une décision du Hub.
type Request struct {
	WorkspaceID    string `json:"workspace_id"`
	HubUserID      string `json:"hub_user_id"`
	Action         string `json:"action"`
	ActorHubUserID string `json:"actor_hub_user_id"`
	Reason         string `json:"reason"`
	Reinstate      bool   `json:"reinstate"`
}

// Result : ce qui a changé.
type Result struct {
	WorkspaceID string `json:"workspace_id"`
	HubUserID   string `json:"hub_user_id"`
	RoleBefore  string `json:"role_before"`
	RoleAfter   string `json:"role_after"`
	Changed     bool   `json:"changed"`
}

// Member : un humain de l'espace, vu par le Hub. Aucune adresse.
type Member struct {
	HubUserID      *string `json:"hub_user_id"` // null : compte non relié au Hub
	Role           string  `json:"role"`
	AdminGranted   bool    `json:"admin_granted"`
	AdminWithdrawn bool    `json:"admin_withdrawn"`
}

// refusal : un refus lisible par le Hub (code machine, statut).
type refusal struct {
	code   string
	status int
}

func (e *refusal) Error() string { return e.code }

var (
	errTenant    = &refusal{"tenant_not_found", http.StatusNotFound}
	errNotInApp  = &refusal{"user_not_in_app", http.StatusNotFound}
	errNotMember = &refusal{"not_a_member", http.StatusNotFound}
	errWithdrawn = &refusal{"admin_withdrawn", http.StatusConflict}
	errIsOwner   = &refusal{"is_owner", http.StatusConflict}
)

// Store : la base système.
type Store struct{ DB *sql.DB }

// Apply décide et écrit dans une seule transaction, ligne d'appartenance verrouillée. Le journal part avec la décision ; un refus est journalisé aussi.
func (s *Store) Apply(ctx context.Context, q Request) (Result, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := decide(ctx, tx, q)
	var no *refusal
	outcome := "ok"
	switch {
	case errors.As(err, &no):
		outcome = no.code
	case err != nil:
		return Result{}, err
	case !res.Changed:
		outcome = "unchanged"
	}
	if _, jerr := tx.ExecContext(ctx, `INSERT INTO veridian_workspace_admin_events
		(workspace_id, hub_user_id, action, result, role_before, role_after, actor_hub_user_id, reason, reinstate)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, q.WorkspaceID, q.HubUserID, q.Action, outcome, res.RoleBefore, res.RoleAfter,
		q.ActorHubUserID, q.Reason, q.Reinstate); jerr != nil {
		return Result{}, jerr
	}
	if cerr := tx.Commit(); cerr != nil {
		return Result{}, cerr
	}
	if no != nil {
		return res, no
	}
	return res, nil
}

func decide(ctx context.Context, tx *sql.Tx, q Request) (Result, error) {
	res := Result{WorkspaceID: q.WorkspaceID, HubUserID: q.HubUserID}
	var live bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces w LEFT JOIN veridian_plan p ON p.workspace_id = w.id
		WHERE w.id = $1 AND p.deleted_at IS NULL)`, q.WorkspaceID).Scan(&live); err != nil {
		return res, err
	}
	if !live {
		return res, errTenant
	}
	// Un compte supprimé au Hub n'a rien à gagner ici ; une clé API n'est pas une personne.
	var userID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE hub_user_id = $1 AND type = 'user' AND hub_deleted_at IS NULL`, q.HubUserID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return res, errNotInApp
	}
	if err != nil {
		return res, err
	}
	// La désignation se lit AVANT l'appartenance : quelqu'un qui a été retiré l'apprend, au lieu d'un simple « pas membre ».
	var known, standing bool
	err = tx.QueryRowContext(ctx, `SELECT revoked_at IS NULL FROM veridian_workspace_admins WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`,
		q.WorkspaceID, userID).Scan(&standing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	known = err == nil
	err = tx.QueryRowContext(ctx, `SELECT role FROM user_workspaces WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`,
		q.WorkspaceID, userID).Scan(&res.RoleBefore)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return res, err
	}
	member := err == nil
	res.RoleAfter = res.RoleBefore
	held := res.RoleBefore == domain.VeridianRoleOwner || res.RoleBefore == domain.VeridianRoleAdmin

	if q.Action == actionGrant {
		if known && !(standing && held) && !q.Reinstate {
			return res, errWithdrawn
		}
		if !member {
			return res, errNotMember
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_workspace_admins (workspace_id, user_id) VALUES ($1,$2)
			ON CONFLICT (workspace_id, user_id) DO UPDATE SET granted_at = NOW(), revoked_at = NULL
			WHERE veridian_workspace_admins.revoked_at IS NOT NULL`, q.WorkspaceID, userID); err != nil {
			return res, err
		}
		if res.RoleBefore == domain.VeridianRoleMember {
			// Conditionnel : seul un membre monte, jamais une ligne n'est créée, jamais un propriétaire n'est touché.
			n, err := setRole(ctx, tx, q.WorkspaceID, userID, domain.VeridianRoleMember, domain.VeridianRoleAdmin)
			if err != nil {
				return res, err
			}
			if n != 1 {
				return res, errors.New("wsadmin: membership changed under lock")
			}
			res.RoleAfter = domain.VeridianRoleAdmin
		}
		res.Changed = !(known && standing && held)
		return res, nil
	}

	if res.RoleBefore == domain.VeridianRoleOwner {
		return res, errIsOwner
	}
	if !known && res.RoleBefore != domain.VeridianRoleAdmin {
		if !member {
			return res, errNotMember
		}
		return res, nil // jamais désigné : rien à retirer, et aucune trace qui l'empêcherait d'être désigné plus tard
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_workspace_admins (workspace_id, user_id, revoked_at) VALUES ($1,$2,NOW())
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET revoked_at = COALESCE(veridian_workspace_admins.revoked_at, NOW())`, q.WorkspaceID, userID); err != nil {
		return res, err
	}
	n, err := setRole(ctx, tx, q.WorkspaceID, userID, domain.VeridianRoleAdmin, domain.VeridianRoleMember)
	if err != nil {
		return res, err
	}
	if n == 1 {
		res.RoleAfter = domain.VeridianRoleMember
	}
	res.Changed = n == 1 || standing
	return res, nil
}

// setRole ne change que la ligne qui porte le rôle attendu, avec toutes les permissions (un admin rétrogradé reste le membre à tous droits qu'il était).
func setRole(ctx context.Context, tx *sql.Tx, ws, userID, from, to string) (int64, error) {
	r, err := tx.ExecContext(ctx, `UPDATE user_workspaces SET role = $4, updated_at = NOW() WHERE workspace_id = $1 AND user_id = $2 AND role = $3`, ws, userID, from, to)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// List : les humains de l'espace. Les membres retirés gardent une ligne tant que leur désignation est connue (rôle vide).
func (s *Store) List(ctx context.Context, workspaceID string) ([]Member, error) {
	var live bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces w LEFT JOIN veridian_plan p ON p.workspace_id = w.id
		WHERE w.id = $1 AND p.deleted_at IS NULL)`, workspaceID).Scan(&live); err != nil {
		return nil, err
	}
	if !live {
		return nil, errTenant
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT u.hub_user_id, COALESCE(uw.role, ''), COALESCE(a.revoked_at IS NULL AND a.user_id IS NOT NULL, FALSE),
			COALESCE(uw.role IN ('owner', 'admin'), FALSE), a.user_id IS NOT NULL
		FROM users u
		LEFT JOIN user_workspaces uw ON uw.user_id = u.id AND uw.workspace_id = $1
		LEFT JOIN veridian_workspace_admins a ON a.user_id = u.id AND a.workspace_id = $1
		WHERE u.type = 'user' AND (uw.user_id IS NOT NULL OR a.user_id IS NOT NULL)
		ORDER BY uw.role DESC NULLS LAST, u.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Member{}
	for rows.Next() {
		var m Member
		var hub sql.NullString
		var standing, held, known bool
		if err := rows.Scan(&hub, &m.Role, &standing, &held, &known); err != nil {
			return nil, err
		}
		if hub.Valid && hub.String != "" {
			m.HubUserID = &hub.String
		}
		m.AdminGranted, m.AdminWithdrawn = standing && held, known && !(standing && held)
		out = append(out, m)
	}
	return out, rows.Err()
}

// validActor : une personne du Hub (uuid) ou un outil du Hub nommé par sa clé (« cle:ADMIN_SECRET »), jamais une adresse.
func validActor(a string) error {
	if _, err := uuid.Parse(a); err == nil {
		return nil
	}
	if name, ok := strings.CutPrefix(a, "cle:"); ok && len(name) > 0 && len(name) <= 64 && strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789") == "" {
		return nil
	}
	return errors.New("actor_hub_user_id must be a uuid or cle:<NAME>")
}

// Handler : les deux routes. Le middleware HMAC est posé par Mount.
type Handler struct {
	Store *Store
	Log   logger.Logger
}

// Post applique une décision du Hub.
func (h *Handler) Post(w http.ResponseWriter, r *http.Request) {
	var q Request
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // une adresse, ou un champ mal écrit, est refusé au lieu d'être ignoré
	if err := dec.Decode(&q); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	q.Reason = strings.TrimSpace(q.Reason)
	_, badUser := uuid.Parse(q.HubUserID)
	badActor := validActor(q.ActorHubUserID)
	switch {
	case q.WorkspaceID == "" || q.Reason == "" || len(q.Reason) > maxReason:
		writeError(w, http.StatusBadRequest, "invalid_payload", "workspace_id and reason (500 max) are required")
		return
	case badUser != nil || badActor != nil:
		writeError(w, http.StatusBadRequest, "invalid_payload", "hub_user_id must be a uuid, actor_hub_user_id a uuid or cle:<NAME>")
		return
	case q.Action != actionGrant && q.Action != actionRevoke:
		writeError(w, http.StatusBadRequest, "invalid_payload", "action must be grant or revoke")
		return
	}
	res, err := h.Store.Apply(r.Context(), q)
	log := h.Log.WithFields(map[string]interface{}{"workspace_id": q.WorkspaceID, "hub_user_id": q.HubUserID, "action": q.Action,
		"actor_hub_user_id": q.ActorHubUserID, "role_before": res.RoleBefore, "role_after": res.RoleAfter, "changed": res.Changed, "reinstate": q.Reinstate})
	var no *refusal
	if errors.As(err, &no) {
		log.WithField("refused", no.code).Warn("wsadmin: décision refusée")
		writeError(w, no.status, no.code, "")
		return
	}
	if err != nil {
		log.WithField("error", err.Error()).Error("wsadmin: décision en échec")
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	log.Info("wsadmin: décision du Hub appliquée")
	writeJSON(w, http.StatusOK, res)
}

// Get relit les rôles d'un espace.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ws := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if ws == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "workspace_id is required")
		return
	}
	members, err := h.Store.List(r.Context(), ws)
	var no *refusal
	if errors.As(err, &no) {
		writeError(w, no.status, no.code, "")
		return
	}
	if err != nil {
		h.Log.WithField("workspace_id", ws).WithField("error", err.Error()).Error("wsadmin: lecture en échec")
		writeError(w, http.StatusInternalServerError, "internal_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"workspace_id": ws, "members": members})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, hint string) {
	body := map[string]string{"error": code, "code": code}
	if hint != "" {
		body["hint"] = hint
	}
	writeJSON(w, status, body)
}
