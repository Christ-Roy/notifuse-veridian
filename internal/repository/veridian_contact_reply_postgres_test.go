package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupContactReplyTest(t *testing.T) (*mocks.MockWorkspaceRepository, *veridianContactReplyRepository, sqlmock.Sqlmock, *sql.DB, func()) {
	ctrl := gomock.NewController(t)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	repo := NewVeridianContactReplyRepository(mockWorkspaceRepo)

	cleanup := func() {
		_ = db.Close()
		ctrl.Finish()
	}
	return mockWorkspaceRepo, repo.(*veridianContactReplyRepository), mock, db, cleanup
}

func TestNewVeridianContactReplyRepository_Constructor(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := NewVeridianContactReplyRepository(mocks.NewMockWorkspaceRepository(ctrl))
	require.NotNil(t, repo)
	var _ domain.VeridianContactReplyRepository = repo
}

func TestVeridianContactReplyRepository_MarkReplied(t *testing.T) {
	ctx := context.Background()
	const ws = "ws1"
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

	t.Run("insert with matched message id, email lowercased", func(t *testing.T) {
		wsRepo, repo, mock, db, cleanup := setupContactReplyTest(t)
		defer cleanup()

		wsRepo.EXPECT().GetConnection(ctx, ws).Return(db, nil)
		mock.ExpectExec(`INSERT INTO veridian_contact_reply`).
			WithArgs("prospect@acme.fr", now, "message_id", sql.NullString{String: "msg-1", Valid: true}).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.MarkReplied(ctx, ws, &domain.VeridianContactReply{
			ContactEmail:     "Prospect@Acme.FR",
			RepliedAt:        now,
			MatchType:        domain.VeridianReplyMatchMessageID,
			MatchedMessageID: "msg-1",
		})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fallback match: matched_message_id NULL", func(t *testing.T) {
		wsRepo, repo, mock, db, cleanup := setupContactReplyTest(t)
		defer cleanup()

		wsRepo.EXPECT().GetConnection(ctx, ws).Return(db, nil)
		mock.ExpectExec(`INSERT INTO veridian_contact_reply`).
			WithArgs("a@b.io", now, "sender_fallback", sql.NullString{}).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.MarkReplied(ctx, ws, &domain.VeridianContactReply{
			ContactEmail: "a@b.io",
			RepliedAt:    now,
			MatchType:    domain.VeridianReplyMatchSenderFallback,
		})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("idempotent: ON CONFLICT DO NOTHING (0 rows affected) is not an error", func(t *testing.T) {
		wsRepo, repo, mock, db, cleanup := setupContactReplyTest(t)
		defer cleanup()

		wsRepo.EXPECT().GetConnection(ctx, ws).Return(db, nil)
		mock.ExpectExec(`INSERT INTO veridian_contact_reply`).
			WithArgs("a@b.io", now, "message_id", sql.NullString{String: "x", Valid: true}).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkReplied(ctx, ws, &domain.VeridianContactReply{
			ContactEmail:     "a@b.io",
			RepliedAt:        now,
			MatchType:        domain.VeridianReplyMatchMessageID,
			MatchedMessageID: "x",
		})
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("nil reply rejected without query", func(t *testing.T) {
		_, repo, _, _, cleanup := setupContactReplyTest(t)
		defer cleanup()
		err := repo.MarkReplied(ctx, ws, nil)
		require.Error(t, err)
	})

	t.Run("empty email rejected without query", func(t *testing.T) {
		_, repo, _, _, cleanup := setupContactReplyTest(t)
		defer cleanup()
		err := repo.MarkReplied(ctx, ws, &domain.VeridianContactReply{ContactEmail: "   "})
		require.Error(t, err)
	})

	t.Run("connection error surfaced", func(t *testing.T) {
		wsRepo, repo, _, _, cleanup := setupContactReplyTest(t)
		defer cleanup()
		wsRepo.EXPECT().GetConnection(ctx, ws).Return(nil, errors.New("boom"))
		err := repo.MarkReplied(ctx, ws, &domain.VeridianContactReply{
			ContactEmail: "a@b.io", RepliedAt: now, MatchType: domain.VeridianReplyMatchMessageID,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "workspace connection")
	})
}

func TestVeridianContactReplyRepository_HasReplied(t *testing.T) {
	ctx := context.Background()
	const ws = "ws1"

	t.Run("exists true (email lowercased)", func(t *testing.T) {
		wsRepo, repo, mock, db, cleanup := setupContactReplyTest(t)
		defer cleanup()

		wsRepo.EXPECT().GetConnection(ctx, ws).Return(db, nil)
		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("prospect@acme.fr").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		ok, err := repo.HasReplied(ctx, ws, "Prospect@ACME.fr")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exists false", func(t *testing.T) {
		wsRepo, repo, mock, db, cleanup := setupContactReplyTest(t)
		defer cleanup()

		wsRepo.EXPECT().GetConnection(ctx, ws).Return(db, nil)
		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("a@b.io").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		ok, err := repo.HasReplied(ctx, ws, "a@b.io")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty email short-circuits to false without query", func(t *testing.T) {
		_, repo, _, _, cleanup := setupContactReplyTest(t)
		defer cleanup()
		ok, err := repo.HasReplied(ctx, ws, "   ")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("query error surfaced", func(t *testing.T) {
		wsRepo, repo, mock, db, cleanup := setupContactReplyTest(t)
		defer cleanup()
		wsRepo.EXPECT().GetConnection(ctx, ws).Return(db, nil)
		mock.ExpectQuery(`SELECT EXISTS`).
			WithArgs("a@b.io").
			WillReturnError(errors.New("db down"))
		ok, err := repo.HasReplied(ctx, ws, "a@b.io")
		require.Error(t, err)
		assert.False(t, ok)
	})
}
