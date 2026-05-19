package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

const idempGetSQL = `
		SELECT key, endpoint, tenant_id, request_hash, response_status, response_body,
		       created_at, expires_at
		FROM veridian_idempotency_keys
		WHERE key = $1 AND expires_at > NOW()
	`

const idempSaveSQL = `
		INSERT INTO veridian_idempotency_keys (
			key, endpoint, tenant_id, request_hash, response_status, response_body,
			created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

const idempDeleteExpiredSQL = `DELETE FROM veridian_idempotency_keys WHERE expires_at < NOW()`

// TestNewVeridianIdempotencyRepository_Constructor verifie que le constructor
// retourne bien une instance qui implemente l'interface domain. C'est un
// garde-fou contre les drifts de signature (changement de pointeur, etc.)
// qui ferait sauter la compilation seulement au moment du wiring app.go.
func TestNewVeridianIdempotencyRepository_Constructor(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := NewVeridianIdempotencyRepository(db)
	require.NotNil(t, repo, "constructor doit retourner une instance non-nil")

	// Verifie l'interface satisfaction implicite (sinon erreur compile dans
	// le test). Si l'interface change, ce test casse au build.
	var _ domain.VeridianIdempotencyRepository = repo
}

func TestVeridianIdempotencyRepository_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		now := time.Now().UTC()
		expires := now.Add(24 * time.Hour)
		body := []byte(`{"workspace_id":"ws-1","created":true}`)

		rows := sqlmock.NewRows([]string{
			"key", "endpoint", "tenant_id", "request_hash", "response_status",
			"response_body", "created_at", "expires_at",
		}).AddRow("k1", "/api/tenants/provision", "ws-1", "hash123", 200, body, now, expires)

		mock.ExpectQuery(idempGetSQL).WithArgs("k1").WillReturnRows(rows)

		e, err := repo.Get(ctx, "k1")
		require.NoError(t, err)
		assert.Equal(t, "k1", e.Key)
		assert.Equal(t, "/api/tenants/provision", e.Endpoint)
		assert.Equal(t, "ws-1", e.TenantID)
		assert.Equal(t, 200, e.ResponseStatus)
		assert.Equal(t, body, e.ResponseBody)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("found with nil tenant_id (admin endpoint)", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		now := time.Now().UTC()
		rows := sqlmock.NewRows([]string{
			"key", "endpoint", "tenant_id", "request_hash", "response_status",
			"response_body", "created_at", "expires_at",
		}).AddRow("k2", "/api/veridian/admin/wipe-test-tenants", nil, "h", 200, []byte(`{}`), now, now.Add(time.Hour))

		mock.ExpectQuery(idempGetSQL).WithArgs("k2").WillReturnRows(rows)

		e, err := repo.Get(ctx, "k2")
		require.NoError(t, err)
		assert.Empty(t, e.TenantID)
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		mock.ExpectQuery(idempGetSQL).WithArgs("missing").WillReturnError(sql.ErrNoRows)

		_, err := repo.Get(ctx, "missing")
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})

	t.Run("expired entries are filtered out (expires_at > NOW filter)", func(t *testing.T) {
		// La SQL WHERE clause exclut deja les entries expirees, donc sqlmock
		// avec ErrNoRows reproduit le comportement attendu : le caller voit
		// une cle expiree comme inexistante (et peut re-executer le handler).
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		mock.ExpectQuery(idempGetSQL).WithArgs("expired").WillReturnError(sql.ErrNoRows)

		_, err := repo.Get(ctx, "expired")
		assert.ErrorIs(t, err, sql.ErrNoRows, "entry expiree → traite comme absente")
	})
}

func TestVeridianIdempotencyRepository_Save(t *testing.T) {
	ctx := context.Background()

	t.Run("insert with tenant_id", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		now := time.Now().UTC()
		entry := &domain.VeridianIdempotencyEntry{
			Key:            "k1",
			Endpoint:       "/api/tenants/provision",
			TenantID:       "ws-1",
			RequestHash:    "h",
			ResponseStatus: 200,
			ResponseBody:   []byte(`{}`),
			CreatedAt:      now,
			ExpiresAt:      now.Add(24 * time.Hour),
		}

		mock.ExpectExec(idempSaveSQL).
			WithArgs("k1", "/api/tenants/provision", "ws-1", "h", 200, []byte(`{}`),
				sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Save(ctx, entry)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert without tenant_id passes nil", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		entry := &domain.VeridianIdempotencyEntry{
			Key:            "k2",
			Endpoint:       "/api/veridian/admin/wipe-test-tenants",
			RequestHash:    "h",
			ResponseStatus: 200,
			ResponseBody:   []byte(`{}`),
			ExpiresAt:      time.Now().Add(time.Hour),
		}

		mock.ExpectExec(idempSaveSQL).
			WithArgs("k2", "/api/veridian/admin/wipe-test-tenants", nil, "h", 200, []byte(`{}`),
				sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Save(ctx, entry)
		require.NoError(t, err)
	})

	t.Run("rejects nil entry", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		err := repo.Save(ctx, nil)
		assert.ErrorContains(t, err, "entry required")
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		cases := []struct {
			name  string
			entry *domain.VeridianIdempotencyEntry
			err   string
		}{
			{"missing key", &domain.VeridianIdempotencyEntry{Endpoint: "/p", RequestHash: "h", ExpiresAt: time.Now().Add(time.Hour)}, "key required"},
			{"missing endpoint", &domain.VeridianIdempotencyEntry{Key: "k", RequestHash: "h", ExpiresAt: time.Now().Add(time.Hour)}, "endpoint required"},
			{"missing hash", &domain.VeridianIdempotencyEntry{Key: "k", Endpoint: "/p", ExpiresAt: time.Now().Add(time.Hour)}, "request_hash required"},
			{"missing expires_at", &domain.VeridianIdempotencyEntry{Key: "k", Endpoint: "/p", RequestHash: "h"}, "expires_at required"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := repo.Save(ctx, tc.entry)
				assert.ErrorContains(t, err, tc.err)
			})
		}
	})

	t.Run("propagates pk conflict (race)", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		entry := &domain.VeridianIdempotencyEntry{
			Key:            "k1",
			Endpoint:       "/api/tenants/provision",
			RequestHash:    "h",
			ResponseStatus: 200,
			ResponseBody:   []byte(`{}`),
			ExpiresAt:      time.Now().Add(time.Hour),
		}

		mock.ExpectExec(idempSaveSQL).WillReturnError(assert.AnError)

		err := repo.Save(ctx, entry)
		assert.Error(t, err)
	})
}

func TestVeridianIdempotencyRepository_DeleteExpired(t *testing.T) {
	ctx := context.Background()

	t.Run("returns count of deleted rows", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		mock.ExpectExec(idempDeleteExpiredSQL).WillReturnResult(sqlmock.NewResult(0, 42))

		n, err := repo.DeleteExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(42), n)
	})

	t.Run("propagates DB error", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIdempotencyRepository(db)

		mock.ExpectExec(idempDeleteExpiredSQL).WillReturnError(assert.AnError)

		_, err := repo.DeleteExpired(ctx)
		assert.Error(t, err)
	})
}
