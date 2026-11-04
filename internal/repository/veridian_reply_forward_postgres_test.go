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

func newReplyForwardRepoMock(t *testing.T) (domain.VeridianReplyForwardRepository, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewVeridianReplyForwardRepositoryWithDB(db), mock
}

func TestVeridianReplyForwardRepo_Claim_Inserted(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectExec(`INSERT INTO veridian_reply_forward[\s\S]*ON CONFLICT \(reply_key\) DO NOTHING`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	ins, dup, capped, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p", Recipients: 1}, 50, time.Now())
	require.NoError(t, err)
	assert.True(t, ins)
	assert.False(t, dup)
	assert.False(t, capped)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Claim_Duplicate(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectExec(`INSERT INTO veridian_reply_forward`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT 1 FROM veridian_reply_forward WHERE reply_key`).WithArgs("k").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	ins, dup, capped, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p"}, 50, time.Now())
	require.NoError(t, err)
	assert.False(t, ins)
	assert.True(t, dup)
	assert.False(t, capped)
}

func TestVeridianReplyForwardRepo_Claim_Capped(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectExec(`INSERT INTO veridian_reply_forward`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT 1 FROM veridian_reply_forward WHERE reply_key`).WithArgs("k").WillReturnError(sql.ErrNoRows)
	ins, dup, capped, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p"}, 50, time.Now())
	require.NoError(t, err)
	assert.False(t, ins)
	assert.False(t, dup)
	assert.True(t, capped)
}

func TestVeridianReplyForwardRepo_Claim_DBError(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectExec(`INSERT INTO veridian_reply_forward`).WillReturnError(errors.New("boom"))
	_, _, _, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k"}, 50, time.Now())
	assert.Error(t, err)
}

func TestVeridianReplyForwardRepo_Finish(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectExec(`UPDATE veridian_reply_forward SET status`).WithArgs("k", "failed", "timeout", nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Finish(context.Background(), "ws", "k", domain.VeridianReplyForwardStatusFailed, "timeout", time.Now()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_ResolveSentMessage_ByID(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectQuery(`SELECT COALESCE\(veridian_profile_id, ''\), COALESCE\(veridian_sender_email, ''\) FROM message_history WHERE id = \$1 OR id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"p", "s"}).AddRow("prof1", "pro@send.example"))
	ref, found, err := repo.ResolveSentMessage(context.Background(), "ws", "uuid-1", "x@y.fr")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "prof1", ref.ProfileID)
	assert.Equal(t, "pro@send.example", ref.SenderEmail)
}

func TestVeridianReplyForwardRepo_ResolveSentMessage_FallbackToContact(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectQuery(`FROM message_history WHERE id = \$1 OR id = \$2`).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM message_history\s+WHERE lower\(contact_email\) = \$1`).WithArgs("x@y.fr").
		WillReturnRows(sqlmock.NewRows([]string{"p", "s"}).AddRow("prof2", "a@b.fr"))
	ref, found, err := repo.ResolveSentMessage(context.Background(), "ws", "uuid-unknown", "X@Y.fr")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "prof2", ref.ProfileID)
}

func TestVeridianReplyForwardRepo_ResolveSentMessage_NotFound(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectQuery(`FROM message_history\s+WHERE lower\(contact_email\)`).WillReturnError(sql.ErrNoRows)
	_, found, err := repo.ResolveSentMessage(context.Background(), "ws", "", "x@y.fr")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestVeridianReplyForwardRepo_Stats(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectQuery(`SELECT profile_id, status, COUNT\(\*\) FROM veridian_reply_forward`).
		WillReturnRows(sqlmock.NewRows([]string{"p", "s", "c"}).AddRow("prof1", "sent", 3).AddRow("prof1", "failed", 1))
	stats, err := repo.Stats(context.Background(), "ws", time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	require.Len(t, stats, 2)
	assert.Equal(t, 3, stats[0].Count)
	assert.Equal(t, "failed", stats[1].Status)
}
