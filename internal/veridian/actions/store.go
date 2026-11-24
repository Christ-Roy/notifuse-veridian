package actions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mcp"
)

// SQLStore : Store sur la base système. Les lignes périmées partent à l'usage, aucune tâche planifiée.
type SQLStore struct{ DB *sql.DB }

func (s *SQLStore) ConsumeJTI(ctx context.Context, jti string) (bool, error) {
	// Le jeton vit 120 s : 10 minutes de mémoire suffisent.
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM veridian_action_jti WHERE seen_at < NOW() - INTERVAL '10 minutes'`); err != nil {
		return false, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO veridian_action_jti (jti) VALUES ($1) ON CONFLICT DO NOTHING`, jti)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Resolve : l'humain vivant porteur de hub_user_id, membre de l'espace relié à CE tenant. Jamais d'espace par défaut, jamais un slug :
// le jeton désigne l'organisation, l'app n'en choisit pas une autre.
func (s *SQLStore) Resolve(ctx context.Context, hubUserID, hubTenantID string) (*mcp.Member, error) {
	var ws, status string
	err := s.DB.QueryRowContext(ctx, `
		SELECT p.workspace_id, p.status FROM veridian_plan p JOIN workspaces w ON w.id = p.workspace_id
		WHERE p.hub_tenant_id = $1 AND p.deleted_at IS NULL ORDER BY p.workspace_id LIMIT 1`, hubTenantID).Scan(&ws, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnlinkedTenant
	}
	if err != nil {
		return nil, err
	}
	m := &mcp.Member{}
	err = s.DB.QueryRowContext(ctx,
		`SELECT id, email FROM users WHERE hub_user_id::text = $1 AND type = 'user' AND hub_deleted_at IS NULL`, hubUserID).Scan(&m.UserID, &m.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownUser
	}
	if err != nil {
		return nil, err
	}
	uw := &domain.UserWorkspace{UserID: m.UserID, WorkspaceID: ws}
	var perms domain.UserPermissions
	var frozen bool
	err = s.DB.QueryRowContext(ctx, `
		SELECT uw.role, uw.permissions,
			EXISTS (SELECT 1 FROM veridian_frozen_members f WHERE f.workspace_id = $1 AND f.user_id = $2::text)
		FROM user_workspaces uw WHERE uw.workspace_id = $1 AND uw.user_id = $2::uuid`, ws, m.UserID).Scan(&uw.Role, &perms, &frozen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	if status != string(domain.PlanStatusActive) || frozen {
		return nil, ErrSuspended
	}
	uw.Permissions = perms
	m.Workspace = uw
	return m, nil
}

func (s *SQLStore) Begin(ctx context.Context, ws, key, action, hash string, o Origin) (json.RawMessage, error) {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM veridian_action_done WHERE created_at < NOW() - INTERVAL '30 days'`); err != nil {
		return nil, err
	}
	// Une réservation sans résultat depuis 2 minutes est un geste mort (processus coupé) : on la reprend, l'import dédoublonne.
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO veridian_action_done (workspace_id, app, idem_key, action, request_hash, hub_user_id, jti)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, app, idem_key) DO UPDATE SET jti = EXCLUDED.jti, hub_user_id = EXCLUDED.hub_user_id, created_at = NOW()
		WHERE veridian_action_done.result IS NULL AND veridian_action_done.request_hash = EXCLUDED.request_hash
			AND veridian_action_done.created_at < NOW() - INTERVAL '2 minutes'`,
		ws, o.App, key, action, hash, o.HubUserID, o.JTI)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return nil, err
	}
	var seen string
	var result []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT request_hash, result FROM veridian_action_done WHERE workspace_id = $1 AND app = $2 AND idem_key = $3`,
		ws, o.App, key).Scan(&seen, &result); err != nil {
		return nil, err
	}
	if seen != hash {
		return nil, ErrKeyReused
	}
	if result == nil {
		return nil, ErrInProgress
	}
	return json.RawMessage(result), nil
}

func (s *SQLStore) Finish(ctx context.Context, ws, app, key string, result json.RawMessage) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE veridian_action_done SET result = $4, done_at = NOW()
		WHERE workspace_id = $1 AND app = $2 AND idem_key = $3`, ws, app, key, []byte(result))
	return err
}

func (s *SQLStore) Release(ctx context.Context, ws, app, key string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM veridian_action_done
		WHERE workspace_id = $1 AND app = $2 AND idem_key = $3 AND result IS NULL`, ws, app, key)
	return err
}
