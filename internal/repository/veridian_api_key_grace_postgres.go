package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianAPIKeyGraceRepository implemente domain.VeridianAPIKeyGraceRepository.
//
// Voir migration V41 + domain/veridian_rotate_transfer.go pour le contexte
// (CONTRAT-HUB §5.15 — grace period 5min apres rotate-api-key).
//
// Pattern aligne sur veridianIdempotencyRepository : systemDB direct, queries
// stateless, errors propagees telles quelles au service caller.
type veridianAPIKeyGraceRepository struct {
	systemDB *sql.DB
}

// NewVeridianAPIKeyGraceRepository cree un repo Postgres pour la table
// veridian_api_key_grace.
func NewVeridianAPIKeyGraceRepository(systemDB *sql.DB) domain.VeridianAPIKeyGraceRepository {
	return &veridianAPIKeyGraceRepository{systemDB: systemDB}
}

// Insert ajoute (ou ecrase) une entree grace. UPSERT sur api_key_user_id :
// idempotent en cas de re-rotate dans la grace period precedente, ce qui
// arrive si Robert lance 2 rotate-api-key d'affilee (le 2eme ecrase le
// revoke_at du 1er, qui devient l'arme du nouveau "ancienne key"). Aucune
// row orpheline ne traine.
func (r *veridianAPIKeyGraceRepository) Insert(ctx context.Context, entry *domain.APIKeyGraceEntry) error {
	if entry == nil {
		return errors.New("entry required")
	}
	if entry.APIKeyUserID == "" {
		return errors.New("api_key_user_id required")
	}
	if entry.WorkspaceID == "" {
		return errors.New("workspace_id required")
	}
	if entry.RevokeAt.IsZero() {
		return errors.New("revoke_at required")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}

	const q = `
		INSERT INTO veridian_api_key_grace (
			api_key_user_id, workspace_id, revoke_at, reason, created_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (api_key_user_id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			revoke_at    = EXCLUDED.revoke_at,
			reason       = EXCLUDED.reason
	`
	_, err := r.systemDB.ExecContext(ctx, q,
		entry.APIKeyUserID, entry.WorkspaceID, entry.RevokeAt, entry.Reason, entry.CreatedAt,
	)
	return err
}

// ListExpired retourne les entrees dont revoke_at <= now. Cap a 100 lignes
// pour eviter de surcharger le cron si une cascade de rotations a deja eu
// lieu (le cron tourne 1×/min, donc il rattrape rapidement le retard).
func (r *veridianAPIKeyGraceRepository) ListExpired(ctx context.Context, now time.Time) ([]*domain.APIKeyGraceEntry, error) {
	const q = `
		SELECT api_key_user_id, workspace_id, revoke_at, COALESCE(reason, ''), created_at
		FROM veridian_api_key_grace
		WHERE revoke_at <= $1
		ORDER BY revoke_at ASC
		LIMIT 100
	`
	rows, err := r.systemDB.QueryContext(ctx, q, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*domain.APIKeyGraceEntry
	for rows.Next() {
		var e domain.APIKeyGraceEntry
		if err := rows.Scan(&e.APIKeyUserID, &e.WorkspaceID, &e.RevokeAt, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// DeleteByID supprime une entree par api_key_user_id. Idempotent : aucune
// erreur si la row n'existe pas (DELETE 0 rows = succes).
func (r *veridianAPIKeyGraceRepository) DeleteByID(ctx context.Context, apiKeyUserID string) error {
	if apiKeyUserID == "" {
		return errors.New("api_key_user_id required")
	}
	const q = `DELETE FROM veridian_api_key_grace WHERE api_key_user_id = $1`
	_, err := r.systemDB.ExecContext(ctx, q, apiKeyUserID)
	return err
}
