package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===
//
// Matcher regexp par défaut (pas newMockSystemDB/QueryMatcherEqual) : les
// requêtes de ce repo sont multi-lignes avec une indentation Go variable,
// un match par sous-chaîne est plus robuste qu'un match caractère-pour-
// caractère sur l'indentation exacte.

func newMockAgentInstallDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestNewAgentInstallTokenRepository_Constructor(t *testing.T) {
	db, _ := newMockAgentInstallDB(t)
	repo := NewAgentInstallTokenRepository(db)
	require.NotNil(t, repo)
	var _ domain.AgentInstallTokenRepository = repo
}

func TestAgentInstallTokenRepository_Insert(t *testing.T) {
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		now := time.Now().UTC()
		rec := &domain.AgentInstallTokenRecord{
			TokenHash:       "hash-1",
			WorkspaceID:     "ws-1",
			APIKeyUserID:    "api-user-1",
			EncryptedAPIKey: "enc-1",
			CreatedBy:       "owner-1",
			ExpiresAt:       now.Add(10 * time.Minute),
		}

		mock.ExpectExec("INSERT INTO agent_install_tokens").
			WithArgs(rec.TokenHash, rec.WorkspaceID, rec.APIKeyUserID, rec.EncryptedAPIKey, rec.CreatedBy, sqlmock.AnyArg(), rec.ExpiresAt).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Insert(ctx, rec)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("nil record", func(t *testing.T) {
		db, _ := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)
		err := repo.Insert(ctx, nil)
		require.Error(t, err)
	})

	t.Run("missing token_hash", func(t *testing.T) {
		db, _ := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)
		err := repo.Insert(ctx, &domain.AgentInstallTokenRecord{WorkspaceID: "ws-1", APIKeyUserID: "u-1", ExpiresAt: time.Now().Add(time.Minute)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token_hash")
	})

	t.Run("missing expires_at", func(t *testing.T) {
		db, _ := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)
		err := repo.Insert(ctx, &domain.AgentInstallTokenRecord{TokenHash: "h", WorkspaceID: "ws-1", APIKeyUserID: "u-1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expires_at")
	})

	t.Run("db error propagates", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		rec := &domain.AgentInstallTokenRecord{
			TokenHash: "hash-1", WorkspaceID: "ws-1", APIKeyUserID: "u-1",
			ExpiresAt: time.Now().Add(time.Minute),
		}
		mock.ExpectExec("INSERT INTO agent_install_tokens").WillReturnError(errors.New("db down"))

		err := repo.Insert(ctx, rec)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
	})
}

func TestAgentInstallTokenRepository_ClaimByHash(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	t.Run("valid unused token is claimed atomically", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		rows := sqlmock.NewRows([]string{
			"token_hash", "workspace_id", "api_key_user_id", "encrypted_api_key",
			"created_by", "created_at", "expires_at", "used_at",
		}).AddRow("hash-1", "ws-1", "api-user-1", "enc-1", "owner-1", now, now.Add(10*time.Minute), now)

		mock.ExpectQuery("UPDATE agent_install_tokens").
			WithArgs("hash-1", sqlmock.AnyArg()).
			WillReturnRows(rows)

		rec, err := repo.ClaimByHash(ctx, "hash-1", now)
		require.NoError(t, err)
		assert.Equal(t, "ws-1", rec.WorkspaceID)
		assert.Equal(t, "enc-1", rec.EncryptedAPIKey)
	})

	t.Run("already used token -> ErrAgentInstallTokenUsed", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		// Claim UPDATE affects 0 rows (used_at already set) -> sql.ErrNoRows
		// from QueryRowContext, then the read-only SELECT discriminates.
		mock.ExpectQuery("UPDATE agent_install_tokens").
			WithArgs("hash-used", sqlmock.AnyArg()).
			WillReturnError(sql.ErrNoRows)
		usedAt := now.Add(-time.Minute)
		mock.ExpectQuery("SELECT used_at, expires_at FROM agent_install_tokens").
			WithArgs("hash-used").
			WillReturnRows(sqlmock.NewRows([]string{"used_at", "expires_at"}).AddRow(usedAt, now.Add(10*time.Minute)))

		_, err := repo.ClaimByHash(ctx, "hash-used", now)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrAgentInstallTokenUsed)
	})

	t.Run("expired token -> ErrAgentInstallTokenExpired", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		mock.ExpectQuery("UPDATE agent_install_tokens").
			WithArgs("hash-expired", sqlmock.AnyArg()).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("SELECT used_at, expires_at FROM agent_install_tokens").
			WithArgs("hash-expired").
			WillReturnRows(sqlmock.NewRows([]string{"used_at", "expires_at"}).AddRow(nil, now.Add(-time.Minute)))

		_, err := repo.ClaimByHash(ctx, "hash-expired", now)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrAgentInstallTokenExpired)
	})

	t.Run("unknown token -> ErrAgentInstallTokenNotFound", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		mock.ExpectQuery("UPDATE agent_install_tokens").
			WithArgs("hash-unknown", sqlmock.AnyArg()).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery("SELECT used_at, expires_at FROM agent_install_tokens").
			WithArgs("hash-unknown").
			WillReturnError(sql.ErrNoRows)

		_, err := repo.ClaimByHash(ctx, "hash-unknown", now)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrAgentInstallTokenNotFound)
	})

	t.Run("empty token_hash rejected without hitting the DB", func(t *testing.T) {
		db, _ := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		_, err := repo.ClaimByHash(ctx, "", now)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token_hash")
	})

	t.Run("genuine DB error on the claim itself propagates (not reclassified)", func(t *testing.T) {
		db, mock := newMockAgentInstallDB(t)
		repo := NewAgentInstallTokenRepository(db)

		mock.ExpectQuery("UPDATE agent_install_tokens").
			WithArgs("hash-1", sqlmock.AnyArg()).
			WillReturnError(errors.New("connection reset"))

		_, err := repo.ClaimByHash(ctx, "hash-1", now)
		require.Error(t, err)
		assert.NotErrorIs(t, err, domain.ErrAgentInstallTokenNotFound)
		assert.Contains(t, err.Error(), "connection reset")
	})
}
