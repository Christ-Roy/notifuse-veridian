package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianFrozenMemberRepository implemente domain.VeridianFrozenMemberRepository.
//
// Voir migration V47 + domain/veridian_freeze.go pour le contexte
// (CONTRAT-HUB §5.21 — seat overage soft warning, freeze per-user).
//
// Pattern aligne sur veridianAPIKeyGraceRepository : systemDB direct, queries
// stateless, erreurs propagees telles quelles au service caller. Hot path
// IsFrozen optimise pour le middleware paywall (1 round-trip par requete
// matchant un workspace Veridian, avec cache 60s en amont).
type veridianFrozenMemberRepository struct {
	systemDB *sql.DB
}

// NewVeridianFrozenMemberRepository cree un repo Postgres pour la table
// veridian_frozen_members.
func NewVeridianFrozenMemberRepository(systemDB *sql.DB) domain.VeridianFrozenMemberRepository {
	return &veridianFrozenMemberRepository{systemDB: systemDB}
}

// Freeze insere ou retourne la row existante. Idempotent strict : si la row
// existe deja (already frozen), on ne touche pas frozen_at — on renvoie la
// row originale et alreadyFrozen=true. Le handler peut alors decider de
// renvoyer 200 idempotent ou 409 conflict selon le contrat.
//
// Pattern INSERT ... ON CONFLICT DO NOTHING + SELECT pour recuperer la ligne
// effective. Une 2eme query est necessaire car ON CONFLICT DO NOTHING ne
// renvoie pas la row existante sans RETURNING combine a CTE — la simplicite
// vaut le round-trip supplementaire (cas rare : 2 freeze consecutifs sur
// le meme membre).
func (r *veridianFrozenMemberRepository) Freeze(ctx context.Context, workspaceID, userID string, reason domain.FreezeReason) (*domain.VeridianFrozenMember, bool, error) {
	if workspaceID == "" {
		return nil, false, errors.New("workspace_id required")
	}
	if userID == "" {
		return nil, false, errors.New("user_id required")
	}
	if reason == "" {
		// Fallback defensif : si le caller oublie la reason, on tag "manual"
		// pour preserver l'audit. Mieux qu'une row sans reason (NULL refuse
		// par NOT NULL en migration).
		reason = domain.FreezeReasonManual
	}

	const insertQ = `
		INSERT INTO veridian_frozen_members (workspace_id, user_id, reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO NOTHING
		RETURNING workspace_id, user_id, frozen_at, reason
	`
	row := r.systemDB.QueryRowContext(ctx, insertQ, workspaceID, userID, string(reason))
	var fm domain.VeridianFrozenMember
	var reasonStr string
	err := row.Scan(&fm.WorkspaceID, &fm.UserID, &fm.FrozenAt, &reasonStr)
	if err == nil {
		fm.Reason = domain.FreezeReason(reasonStr)
		return &fm, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	// Pas de row inseree → already exists. Recuperer la row existante.
	const selectQ = `
		SELECT workspace_id, user_id, frozen_at, reason
		FROM veridian_frozen_members
		WHERE workspace_id = $1 AND user_id = $2
	`
	row = r.systemDB.QueryRowContext(ctx, selectQ, workspaceID, userID)
	if err := row.Scan(&fm.WorkspaceID, &fm.UserID, &fm.FrozenAt, &reasonStr); err != nil {
		return nil, false, err
	}
	fm.Reason = domain.FreezeReason(reasonStr)
	return &fm, true, nil
}

// Unfreeze supprime la row freeze. Idempotent : DELETE 0 rows = (false, nil).
// Retourne (true, nil) si une row a effectivement ete supprimee → permet au
// handler d'emettre le webhook tenant.member_unfrozen uniquement quand un
// vrai changement a eu lieu.
func (r *veridianFrozenMemberRepository) Unfreeze(ctx context.Context, workspaceID, userID string) (bool, error) {
	if workspaceID == "" {
		return false, errors.New("workspace_id required")
	}
	if userID == "" {
		return false, errors.New("user_id required")
	}
	const q = `DELETE FROM veridian_frozen_members WHERE workspace_id = $1 AND user_id = $2`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// IsFrozen est le hot path appele par le middleware paywall. Lookup PK
// composite (workspace_id, user_id) → 1 round-trip O(log n). Retourne
// (frozen, reason, error). En cas de sql.ErrNoRows → (false, "", nil) sans
// propager l'erreur au middleware (case nominal).
func (r *veridianFrozenMemberRepository) IsFrozen(ctx context.Context, workspaceID, userID string) (bool, domain.FreezeReason, error) {
	if workspaceID == "" || userID == "" {
		return false, "", nil
	}
	const q = `SELECT reason FROM veridian_frozen_members WHERE workspace_id = $1 AND user_id = $2`
	var reasonStr string
	err := r.systemDB.QueryRowContext(ctx, q, workspaceID, userID).Scan(&reasonStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, domain.FreezeReason(reasonStr), nil
}
