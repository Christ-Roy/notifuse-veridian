package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// agentInstallTokenRepository implemente domain.AgentInstallTokenRepository.
// Pattern aligne sur veridianAPIKeyGraceRepository : systemDB direct, queries
// stateless. Voir migration V59 + internal/domain/agent_install.go.
type agentInstallTokenRepository struct {
	systemDB *sql.DB
}

// NewAgentInstallTokenRepository cree un repo Postgres pour la table
// agent_install_tokens (base systeme).
func NewAgentInstallTokenRepository(systemDB *sql.DB) domain.AgentInstallTokenRepository {
	return &agentInstallTokenRepository{systemDB: systemDB}
}

func (r *agentInstallTokenRepository) Insert(ctx context.Context, rec *domain.AgentInstallTokenRecord) error {
	if rec == nil {
		return errors.New("record required")
	}
	if rec.TokenHash == "" {
		return errors.New("token_hash required")
	}
	if rec.WorkspaceID == "" {
		return errors.New("workspace_id required")
	}
	if rec.APIKeyUserID == "" {
		return errors.New("api_key_user_id required")
	}
	if rec.ExpiresAt.IsZero() {
		return errors.New("expires_at required")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}

	const q = `
		INSERT INTO agent_install_tokens (
			token_hash, workspace_id, api_key_user_id, encrypted_api_key,
			created_by, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.systemDB.ExecContext(ctx, q,
		rec.TokenHash, rec.WorkspaceID, rec.APIKeyUserID, rec.EncryptedAPIKey,
		rec.CreatedBy, rec.CreatedAt, rec.ExpiresAt,
	)
	return err
}

// ClaimByHash consomme le jeton en une seule UPDATE atomique : la clause
// "WHERE used_at IS NULL" garantit qu'au plus une requête concurrente gagne
// la course (la seconde voit 0 ligne affectée). On distingue ensuite
// "n'existe pas" / "déjà utilisé" / "expiré" par un SELECT de lecture
// seule si l'UPDATE n'a touché aucune ligne — jamais l'inverse (on ne
// lit jamais puis n'écrit jamais : ce serait une fenêtre de course).
func (r *agentInstallTokenRepository) ClaimByHash(ctx context.Context, tokenHash string, now time.Time) (*domain.AgentInstallTokenRecord, error) {
	if tokenHash == "" {
		return nil, errors.New("token_hash required")
	}
	now = now.UTC()

	const claimQ = `
		UPDATE agent_install_tokens
		SET used_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING token_hash, workspace_id, api_key_user_id, encrypted_api_key,
		          created_by, created_at, expires_at, used_at
	`
	rec := &domain.AgentInstallTokenRecord{}
	err := r.systemDB.QueryRowContext(ctx, claimQ, tokenHash, now).Scan(
		&rec.TokenHash, &rec.WorkspaceID, &rec.APIKeyUserID, &rec.EncryptedAPIKey,
		&rec.CreatedBy, &rec.CreatedAt, &rec.ExpiresAt, &rec.UsedAt,
	)
	if err == nil {
		return rec, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// L'UPDATE n'a touché aucune ligne : soit la row n'existe pas, soit elle
	// est déjà utilisée, soit elle est expirée. Un SELECT de lecture seule
	// (hors course, purement informatif) discrimine les trois cas pour un
	// message d'erreur utile.
	const readQ = `
		SELECT used_at, expires_at FROM agent_install_tokens WHERE token_hash = $1
	`
	var usedAt *time.Time
	var expiresAt time.Time
	readErr := r.systemDB.QueryRowContext(ctx, readQ, tokenHash).Scan(&usedAt, &expiresAt)
	if errors.Is(readErr, sql.ErrNoRows) {
		return nil, domain.ErrAgentInstallTokenNotFound
	}
	if readErr != nil {
		return nil, readErr
	}
	if usedAt != nil {
		return nil, domain.ErrAgentInstallTokenUsed
	}
	return nil, domain.ErrAgentInstallTokenExpired
}
