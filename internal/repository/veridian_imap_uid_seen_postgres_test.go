package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

// SQL exact généré par Squirrel (clés sq.Eq triées alphabétiquement). Capturé
// au build du repo ; toute dérive casse le test (gate anti-régression SQL).
const imapFilterUnseenSQL = `SELECT uid FROM veridian_imap_uid_seen WHERE folder = $1 AND integration_id = $2 AND uid IN ($3,$4,$5) AND uid_validity = $6 AND workspace_id = $7`

const imapMarkSeenSQL = `INSERT INTO veridian_imap_uid_seen (workspace_id,integration_id,folder,uid_validity,uid) VALUES ($1,$2,$3,$4,$5),($6,$7,$8,$9,$10) ON CONFLICT (workspace_id, integration_id, folder, uid_validity, uid) DO NOTHING`

func TestNewVeridianIMAPUIDSeenRepository_Constructor(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewVeridianIMAPUIDSeenRepository(db)
	require.NotNil(t, repo)
	var _ domain.VeridianIMAPUIDSeenRepository = repo
}

func TestVeridianIMAPUIDSeenRepository_FilterUnseen(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil without query", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		out, err := repo.FilterUnseen(ctx, "ws1", "int1", "INBOX", 42, nil)
		require.NoError(t, err)
		assert.Empty(t, out)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("subset already seen filtered out", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		// UID 11 is already seen → returned by the SELECT.
		rows := sqlmock.NewRows([]string{"uid"}).AddRow(int64(11))
		mock.ExpectQuery(imapFilterUnseenSQL).
			WithArgs("INBOX", "int1", int64(10), int64(11), int64(12), int64(42), "ws1").
			WillReturnRows(rows)

		out, err := repo.FilterUnseen(ctx, "ws1", "int1", "INBOX", 42, []uint32{10, 11, 12})
		require.NoError(t, err)
		assert.Equal(t, []uint32{10, 12}, out)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("none seen returns all", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		rows := sqlmock.NewRows([]string{"uid"}) // empty result
		mock.ExpectQuery(imapFilterUnseenSQL).
			WithArgs("INBOX", "int1", int64(10), int64(11), int64(12), int64(42), "ws1").
			WillReturnRows(rows)

		out, err := repo.FilterUnseen(ctx, "ws1", "int1", "INBOX", 42, []uint32{10, 11, 12})
		require.NoError(t, err)
		assert.Equal(t, []uint32{10, 11, 12}, out)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error bubbles up", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		mock.ExpectQuery(imapFilterUnseenSQL).
			WithArgs("INBOX", "int1", int64(10), int64(11), int64(12), int64(42), "ws1").
			WillReturnError(errors.New("db down"))

		out, err := repo.FilterUnseen(ctx, "ws1", "int1", "INBOX", 42, []uint32{10, 11, 12})
		require.Error(t, err)
		assert.Nil(t, out)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVeridianIMAPUIDSeenRepository_MarkSeen(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input is noop", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		err := repo.MarkSeen(ctx, "ws1", "int1", "INBOX", 42, nil)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("inserts batch with ON CONFLICT DO NOTHING", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		mock.ExpectExec(imapMarkSeenSQL).
			WithArgs(
				"ws1", "int1", "INBOX", int64(42), int64(10),
				"ws1", "int1", "INBOX", int64(42), int64(11),
			).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := repo.MarkSeen(ctx, "ws1", "int1", "INBOX", 42, []uint32{10, 11})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error bubbles up", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianIMAPUIDSeenRepository(db)

		mock.ExpectExec(imapMarkSeenSQL).
			WithArgs(
				"ws1", "int1", "INBOX", int64(42), int64(10),
				"ws1", "int1", "INBOX", int64(42), int64(11),
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.MarkSeen(ctx, "ws1", "int1", "INBOX", 42, []uint32{10, 11})
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
