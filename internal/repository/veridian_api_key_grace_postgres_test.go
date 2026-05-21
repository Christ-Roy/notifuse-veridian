package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SQL litteraux pour matcher avec QueryMatcherEqual (newMockSystemDB).

const graceInsertSQL = `
		INSERT INTO veridian_api_key_grace (
			api_key_user_id, workspace_id, revoke_at, reason, created_at
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (api_key_user_id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			revoke_at    = EXCLUDED.revoke_at,
			reason       = EXCLUDED.reason
	`

const graceListExpiredSQL = `
		SELECT api_key_user_id, workspace_id, revoke_at, COALESCE(reason, ''), created_at
		FROM veridian_api_key_grace
		WHERE revoke_at <= $1
		ORDER BY revoke_at ASC
		LIMIT 100
	`

const graceDeleteByIDSQL = `DELETE FROM veridian_api_key_grace WHERE api_key_user_id = $1`

func TestNewVeridianAPIKeyGraceRepository_Constructor(t *testing.T) {
	db, _ := newMockSystemDB(t)
	repo := NewVeridianAPIKeyGraceRepository(db)
	require.NotNil(t, repo)
	var _ domain.VeridianAPIKeyGraceRepository = repo
}

func TestVeridianAPIKeyGraceRepository_Insert(t *testing.T) {
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		now := time.Now().UTC()
		revokeAt := now.Add(domain.APIKeyGracePeriod)
		entry := &domain.APIKeyGraceEntry{
			APIKeyUserID: "u-1",
			WorkspaceID:  "ws-1",
			RevokeAt:     revokeAt,
			Reason:       "rotation",
			CreatedAt:    now,
		}

		mock.ExpectExec(graceInsertSQL).
			WithArgs("u-1", "ws-1", revokeAt, "rotation", now).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Insert(ctx, entry)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("created_at backfilled when zero", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		revokeAt := time.Now().UTC().Add(5 * time.Minute)
		entry := &domain.APIKeyGraceEntry{
			APIKeyUserID: "u-1",
			WorkspaceID:  "ws-1",
			RevokeAt:     revokeAt,
			Reason:       "test",
		}

		mock.ExpectExec(graceInsertSQL).
			WithArgs("u-1", "ws-1", revokeAt, "test", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Insert(ctx, entry)
		assert.NoError(t, err)
		assert.False(t, entry.CreatedAt.IsZero(), "created_at doit etre backfille")
	})

	t.Run("nil entry returns error", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)
		err := repo.Insert(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "entry required")
	})

	t.Run("missing api_key_user_id", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)
		err := repo.Insert(ctx, &domain.APIKeyGraceEntry{WorkspaceID: "ws-1", RevokeAt: time.Now()})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "api_key_user_id required")
	})

	t.Run("missing workspace_id", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)
		err := repo.Insert(ctx, &domain.APIKeyGraceEntry{APIKeyUserID: "u-1", RevokeAt: time.Now()})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspace_id required")
	})

	t.Run("missing revoke_at", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)
		err := repo.Insert(ctx, &domain.APIKeyGraceEntry{APIKeyUserID: "u-1", WorkspaceID: "ws-1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "revoke_at required")
	})

	t.Run("db error propagated", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		revokeAt := time.Now().UTC().Add(5 * time.Minute)
		mock.ExpectExec(graceInsertSQL).WillReturnError(errors.New("conn dead"))

		err := repo.Insert(ctx, &domain.APIKeyGraceEntry{
			APIKeyUserID: "u-1",
			WorkspaceID:  "ws-1",
			RevokeAt:     revokeAt,
			CreatedAt:    time.Now().UTC(),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conn dead")
	})
}

func TestVeridianAPIKeyGraceRepository_ListExpired(t *testing.T) {
	ctx := context.Background()

	t.Run("returns expired rows", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		now := time.Now().UTC()
		past := now.Add(-1 * time.Minute)
		rows := sqlmock.NewRows([]string{"api_key_user_id", "workspace_id", "revoke_at", "reason", "created_at"}).
			AddRow("u-1", "ws-1", past, "rot-1", past.Add(-5*time.Minute)).
			AddRow("u-2", "ws-2", past.Add(-30*time.Second), "", past.Add(-6*time.Minute))

		mock.ExpectQuery(graceListExpiredSQL).WithArgs(now).WillReturnRows(rows)

		entries, err := repo.ListExpired(ctx, now)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Equal(t, "u-1", entries[0].APIKeyUserID)
		assert.Equal(t, "ws-1", entries[0].WorkspaceID)
		assert.Equal(t, "rot-1", entries[0].Reason)
		assert.Equal(t, "u-2", entries[1].APIKeyUserID)
		assert.Empty(t, entries[1].Reason)
	})

	t.Run("returns nil when no rows", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		now := time.Now().UTC()
		mock.ExpectQuery(graceListExpiredSQL).WithArgs(now).WillReturnRows(
			sqlmock.NewRows([]string{"api_key_user_id", "workspace_id", "revoke_at", "reason", "created_at"}),
		)

		entries, err := repo.ListExpired(ctx, now)
		require.NoError(t, err)
		assert.Nil(t, entries)
	})

	t.Run("db error propagated", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		now := time.Now().UTC()
		mock.ExpectQuery(graceListExpiredSQL).WithArgs(now).WillReturnError(errors.New("conn dead"))

		_, err := repo.ListExpired(ctx, now)
		require.Error(t, err)
	})
}

func TestVeridianAPIKeyGraceRepository_DeleteByID(t *testing.T) {
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		mock.ExpectExec(graceDeleteByIDSQL).WithArgs("u-1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteByID(ctx, "u-1")
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("idempotent on unknown id", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		mock.ExpectExec(graceDeleteByIDSQL).WithArgs("u-unknown").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteByID(ctx, "u-unknown")
		assert.NoError(t, err)
	})

	t.Run("missing id returns error", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)
		err := repo.DeleteByID(ctx, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "api_key_user_id required")
	})

	t.Run("db error propagated", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianAPIKeyGraceRepository(db)

		mock.ExpectExec(graceDeleteByIDSQL).WillReturnError(errors.New("conn dead"))

		err := repo.DeleteByID(ctx, "u-1")
		require.Error(t, err)
	})
}
