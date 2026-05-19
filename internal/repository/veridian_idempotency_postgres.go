package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianIdempotencyRepository implements domain.VeridianIdempotencyRepository.
//
// Voir migration V35 + domain/veridian_idempotency.go pour le contexte
// (CONTRAT-HUB sec. 5.11).
type veridianIdempotencyRepository struct {
	systemDB *sql.DB
}

// NewVeridianIdempotencyRepository cree un repo Postgres pour la table
// veridian_idempotency_keys.
func NewVeridianIdempotencyRepository(systemDB *sql.DB) domain.VeridianIdempotencyRepository {
	return &veridianIdempotencyRepository{systemDB: systemDB}
}

// Get recupere une entree par cle. Retourne sql.ErrNoRows si absent OU si
// expire (expires_at < NOW) — le cron DeleteExpired n'a pas encore tourne
// pour cette ligne mais on la traite deja comme inexistante.
func (r *veridianIdempotencyRepository) Get(ctx context.Context, key string) (*domain.VeridianIdempotencyEntry, error) {
	const q = `
		SELECT key, endpoint, tenant_id, request_hash, response_status, response_body,
		       created_at, expires_at
		FROM veridian_idempotency_keys
		WHERE key = $1 AND expires_at > NOW()
	`
	var e domain.VeridianIdempotencyEntry
	var tenantID sql.NullString
	if err := r.systemDB.QueryRowContext(ctx, q, key).Scan(
		&e.Key, &e.Endpoint, &tenantID, &e.RequestHash, &e.ResponseStatus, &e.ResponseBody,
		&e.CreatedAt, &e.ExpiresAt,
	); err != nil {
		return nil, err
	}
	if tenantID.Valid {
		e.TenantID = tenantID.String
	}
	return &e, nil
}

// Save INSERT la cle. Si la PK existe deja (race), retourne l'erreur Postgres
// telle quelle (le caller doit catch et fallback en Get + replay).
func (r *veridianIdempotencyRepository) Save(ctx context.Context, e *domain.VeridianIdempotencyEntry) error {
	if e == nil {
		return errors.New("entry required")
	}
	if e.Key == "" {
		return errors.New("key required")
	}
	if e.Endpoint == "" {
		return errors.New("endpoint required")
	}
	if e.RequestHash == "" {
		return errors.New("request_hash required")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.ExpiresAt.IsZero() {
		return errors.New("expires_at required (set by middleware)")
	}

	var tenantIDArg interface{}
	if e.TenantID != "" {
		tenantIDArg = e.TenantID
	}

	const q = `
		INSERT INTO veridian_idempotency_keys (
			key, endpoint, tenant_id, request_hash, response_status, response_body,
			created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.systemDB.ExecContext(ctx, q,
		e.Key, e.Endpoint, tenantIDArg, e.RequestHash, e.ResponseStatus, e.ResponseBody,
		e.CreatedAt, e.ExpiresAt,
	)
	return err
}

// DeleteExpired supprime toutes les entrees dont expires_at < NOW.
// Retourne le nombre de lignes supprimees (pour logs cron).
func (r *veridianIdempotencyRepository) DeleteExpired(ctx context.Context) (int64, error) {
	const q = `DELETE FROM veridian_idempotency_keys WHERE expires_at < NOW()`
	res, err := r.systemDB.ExecContext(ctx, q)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
