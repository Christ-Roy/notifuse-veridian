// Package platformadmin : un administrateur de plateforme est propriétaire EN PLUS du client, jamais à sa place (fiche 95).
// Il est ajouté par son hub_user_id, d'un geste nommé et daté ; aucune autre appartenance n'est touchée. Le client peut le retirer,
// l'inverse est refusé, et un retrait n'est jamais remis tout seul.
package platformadmin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

var (
	ErrWorkspaceUnknown = errors.New("workspace not found")
	ErrPersonUnknown    = errors.New("no live account carries this hub_user_id")
	ErrRemovedByOwner   = errors.New("this person was removed from the workspace, it is never put back")
	ErrNoClientOwner    = errors.New("the workspace has no other human owner")
	ErrOthersChanged    = errors.New("another membership changed during the operation, rolled back")
)

// Member : une appartenance, sans adresse.
type Member struct {
	UserID        string `json:"user_id"`
	HubUserID     string `json:"hub_user_id,omitempty"`
	Role          string `json:"role"`
	Machine       bool   `json:"machine"`
	PlatformAdmin bool   `json:"platform_admin"`
	fingerprint   string // rôle, droits et dates : comparés avant et après
}

// Result : ce qui a été fait, relu dans la transaction.
type Result struct {
	WorkspaceID  string   `json:"workspace_id"`
	HubUserID    string   `json:"hub_user_id"`
	State        string   `json:"state"` // added, promoted, already_admin, already_owner (propriétaire en propre, sans marque)
	PreviousRole string   `json:"previous_role"`
	DryRun       bool     `json:"dry_run"`
	Before       []Member `json:"before"`
	After        []Member `json:"after"`
}

// Grant : le geste.
type Grant struct {
	WorkspaceID, HubUserID, By, Reason string
	DryRun                             bool
}

type Store struct{ DB *sql.DB }

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) (commit bool, err error)) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := fn(tx)
	if err != nil || !ok {
		return err
	}
	return tx.Commit()
}

const membersSQL = `SELECT uw.user_id::text, COALESCE(u.hub_user_id::text, ''), uw.role, u.type <> 'user',
		(pa.user_id IS NOT NULL AND pa.removed_at IS NULL),
		uw.role || '|' || COALESCE(uw.permissions::text, '') || '|' || uw.created_at::text || '|' || uw.updated_at::text
	FROM user_workspaces uw JOIN users u ON u.id = uw.user_id
	LEFT JOIN veridian_platform_admin pa ON pa.workspace_id = uw.workspace_id AND pa.user_id = uw.user_id
	WHERE uw.workspace_id = $1 ORDER BY uw.user_id`

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func members(ctx context.Context, q querier, ws string) ([]Member, error) {
	rows, err := q.QueryContext(ctx, membersSQL, ws)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.HubUserID, &m.Role, &m.Machine, &m.PlatformAdmin, &m.fingerprint); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Members : le relevé d'un espace.
func (s *Store) Members(ctx context.Context, ws string) ([]Member, error) {
	return members(ctx, s.DB, ws)
}

// Add pose la personne propriétaire, en plus des autres. Une seule ligne d'appartenance s'écrit, et le contrôle le relit.
func (s *Store) Add(ctx context.Context, g Grant) (*Result, error) {
	res := &Result{WorkspaceID: g.WorkspaceID, HubUserID: g.HubUserID, DryRun: g.DryRun}
	err := s.tx(ctx, func(tx *sql.Tx) (bool, error) {
		var one int
		// Le verrou de l'espace met en file deux gestes simultanés, et fige les appartenances lues.
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM workspaces WHERE id = $1 FOR UPDATE`, g.WorkspaceID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, ErrWorkspaceUnknown
			}
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM user_workspaces WHERE workspace_id = $1 FOR UPDATE`, g.WorkspaceID); err != nil {
			return false, err
		}
		var userID string
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM users WHERE hub_user_id::text = $1 AND type = 'user' AND hub_deleted_at IS NULL`,
			g.HubUserID).Scan(&userID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, ErrPersonUnknown
			}
			return false, err
		}
		before, err := members(ctx, tx, g.WorkspaceID)
		if err != nil {
			return false, err
		}
		res.Before = before
		role, clientOwners := "", 0
		for _, m := range before {
			switch {
			case m.UserID == userID:
				role = m.Role
			case m.Role == "owner" && !m.Machine && !m.PlatformAdmin:
				clientOwners++
			}
		}
		res.PreviousRole = role

		var removed sql.NullTime
		marked := true
		if err := tx.QueryRowContext(ctx, `SELECT removed_at FROM veridian_platform_admin WHERE workspace_id = $1 AND user_id = $2::uuid`,
			g.WorkspaceID, userID).Scan(&removed); errors.Is(err, sql.ErrNoRows) {
			marked = false
		} else if err != nil {
			return false, err
		}
		switch {
		case marked && (removed.Valid || role != "owner"):
			return false, ErrRemovedByOwner // retiré ou rétrogradé depuis : même si la trace du geste manque, le rôle le dit
		case marked:
			res.State, res.After = "already_admin", before
			return false, nil
		case role == "owner":
			res.State, res.After = "already_owner", before
			return false, nil
		case clientOwners == 0:
			return false, ErrNoClientOwner
		case role == "":
			res.State = "added"
		default:
			res.State = "promoted"
		}

		perms, err := json.Marshal(domain.FullPermissions)
		if err != nil {
			return false, err
		}
		now := time.Now().UTC()
		// updated_at d'une appartenance existante ne bouge pas : ce n'est pas la personne qui a agi.
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_workspaces (user_id, workspace_id, role, permissions, created_at, updated_at)
			VALUES ($1::uuid, $2, 'owner', $3, $4, $4)
			ON CONFLICT (user_id, workspace_id) DO UPDATE SET role = 'owner', permissions = EXCLUDED.permissions`,
			userID, g.WorkspaceID, string(perms), now); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_platform_admin (workspace_id, user_id, hub_user_id, previous_role, granted_by, reason, granted_at)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7)`, g.WorkspaceID, userID, g.HubUserID, role, g.By, g.Reason, now); err != nil {
			return false, err
		}
		if res.After, err = members(ctx, tx, g.WorkspaceID); err != nil {
			return false, err
		}
		if err := onlyThisOneChanged(before, res.After, userID); err != nil {
			return false, err
		}
		return !g.DryRun, nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// onlyThisOneChanged : toutes les autres appartenances sont identiques, droits et dates compris, et celle-ci est propriétaire.
func onlyThisOneChanged(before, after []Member, userID string) error {
	was := map[string]string{}
	for _, m := range before {
		if m.UserID != userID {
			was[m.UserID] = m.fingerprint
		}
	}
	seen, owner := 0, false
	for _, m := range after {
		if m.UserID == userID {
			owner = m.Role == "owner" && m.PlatformAdmin
			continue
		}
		if fp, ok := was[m.UserID]; !ok || fp != m.fingerprint {
			return fmt.Errorf("%w (user %s)", ErrOthersChanged, m.UserID)
		}
		seen++
	}
	if seen != len(was) || !owner {
		return ErrOthersChanged
	}
	return nil
}

// Standing : ce qu'est une personne dans un espace.
type Standing struct {
	Owner         bool
	PlatformAdmin bool
}

func (s *Store) Standing(ctx context.Context, ws, userID string) (Standing, error) {
	var st Standing
	err := s.DB.QueryRowContext(ctx, `SELECT uw.role = 'owner', EXISTS (SELECT 1 FROM veridian_platform_admin pa
			WHERE pa.workspace_id = uw.workspace_id AND pa.user_id = uw.user_id AND pa.removed_at IS NULL)
		FROM user_workspaces uw WHERE uw.workspace_id = $1 AND uw.user_id::text = $2`, ws, userID).Scan(&st.Owner, &st.PlatformAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return Standing{}, nil
	}
	return st, err
}

// MarkRemoved garde la trace du retrait (qui, quand).
func (s *Store) MarkRemoved(ctx context.Context, ws, userID, by string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE veridian_platform_admin SET removed_at = NOW(), removed_by = NULLIF($3, '')::uuid
		WHERE workspace_id = $1 AND user_id::text = $2 AND removed_at IS NULL`, ws, userID, by)
	return err
}

// Admins : les administrateurs de plateforme actifs d'un espace.
func (s *Store) Admins(ctx context.Context, ws string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id::text FROM veridian_platform_admin WHERE workspace_id = $1 AND removed_at IS NULL`, ws)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Gone : cette personne a été administrateur de plateforme de cet espace et n'en fait plus partie.
func (s *Store) Gone(ctx context.Context, ws, hubUserID string) (bool, error) {
	var gone bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM veridian_platform_admin pa WHERE pa.workspace_id = $1 AND pa.hub_user_id = $2
		AND NOT EXISTS (SELECT 1 FROM user_workspaces uw WHERE uw.workspace_id = pa.workspace_id AND uw.user_id = pa.user_id))`, ws, hubUserID).Scan(&gone)
	return gone, err
}
