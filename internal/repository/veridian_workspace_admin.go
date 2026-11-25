package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Le registre des admins d'espace (V82, fiche 95). Un retrait n'a pas besoin d'être noté : une désignation dont la personne ne tient plus le rôle
// (retirée par n'importe quel chemin, rétrogradée) EST un retrait.

var _ domain.VeridianAdminLedger = (*workspaceRepository)(nil)

const veridianAdminStateSQL = `
	SELECT a.revoked_at IS NULL, COALESCE(uw.role IN ('owner', 'admin'), FALSE)
	FROM veridian_workspace_admins a
	LEFT JOIN user_workspaces uw ON uw.workspace_id = a.workspace_id AND uw.user_id = a.user_id
	WHERE a.workspace_id = $1 AND a.user_id = $2`

func (r *workspaceRepository) veridianAdminState(ctx context.Context, workspaceID, userID string) (known, standing, held bool, err error) {
	err = r.systemDB.QueryRowContext(ctx, veridianAdminStateSQL, workspaceID, userID).Scan(&standing, &held)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, false, nil
	}
	return err == nil, standing, held, err
}

func (r *workspaceRepository) VeridianAdminGranted(ctx context.Context, workspaceID, userID string) (bool, error) {
	known, standing, held, err := r.veridianAdminState(ctx, workspaceID, userID)
	return known && standing && held, err
}

func (r *workspaceRepository) VeridianAdminWithdrawn(ctx context.Context, workspaceID, userID string) (bool, error) {
	known, standing, held, err := r.veridianAdminState(ctx, workspaceID, userID)
	return known && !(standing && held), err
}
