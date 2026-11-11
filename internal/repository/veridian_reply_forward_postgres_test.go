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

func expectClaimPrelude(mock sqlmock.Sqlmock, inserted int64) {
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO veridian_reply_forward[\s\S]*'deferred'[\s\S]*ON CONFLICT \(reply_key\) DO NOTHING`).
		WillReturnResult(sqlmock.NewResult(0, inserted))
}

func TestVeridianReplyForwardRepo_Claim_Leased(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	expectClaimPrelude(mock, 1)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM veridian_reply_forward WHERE profile_id = \$1 AND status <> 'deferred'`).
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(3))
	mock.ExpectQuery(`UPDATE veridian_reply_forward SET status = 'retry'[\s\S]*ORDER BY created_at, reply_key LIMIT \$3 FOR UPDATE[\s\S]*RETURNING reply_key`).
		WithArgs("p", sqlmock.AnyArg(), 497).
		WillReturnRows(sqlmock.NewRows([]string{"reply_key"}).AddRow("k"))
	mock.ExpectQuery(`UPDATE veridian_reply_forward SET status = 'pending'[\s\S]*RETURNING attempts`).
		WillReturnRows(sqlmock.NewRows([]string{"attempts"}).AddRow(1))
	mock.ExpectCommit()
	claim, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p", Recipients: 1, Payload: "{}"}, 500, time.Now())
	require.NoError(t, err)
	assert.True(t, claim.Leased)
	assert.False(t, claim.Deferred)
	assert.False(t, claim.Duplicate)
	assert.Equal(t, 1, claim.Attempts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Claim_OverCapIsDeferredNotDropped(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	expectClaimPrelude(mock, 1)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM veridian_reply_forward`).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(50))
	// plafond atteint : aucune promotion, la ligne INSEREE reste deferred, et la transaction est validee
	mock.ExpectCommit()
	claim, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p"}, 50, time.Now())
	require.NoError(t, err)
	assert.True(t, claim.Deferred)
	assert.False(t, claim.Leased)
	assert.False(t, claim.Duplicate)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Claim_OlderDeferredCopiesGoFirst(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	expectClaimPrelude(mock, 1)
	mock.ExpectQuery(`SELECT COUNT\(\*\)`).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(9))
	// une seule place : elle va a la plus ancienne ("old"), pas a la nouvelle ("k")
	mock.ExpectQuery(`UPDATE veridian_reply_forward SET status = 'retry'`).WithArgs("p", sqlmock.AnyArg(), 1).
		WillReturnRows(sqlmock.NewRows([]string{"reply_key"}).AddRow("old"))
	mock.ExpectCommit()
	claim, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p"}, 10, time.Now())
	require.NoError(t, err)
	assert.True(t, claim.Deferred)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Claim_Duplicate(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	expectClaimPrelude(mock, 0)
	mock.ExpectCommit()
	claim, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k", ProfileID: "p"}, 50, time.Now())
	require.NoError(t, err)
	assert.True(t, claim.Duplicate)
	assert.False(t, claim.Leased)
	assert.False(t, claim.Deferred)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Claim_DBError(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO veridian_reply_forward`).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	_, err := repo.Claim(context.Background(), "ws", domain.VeridianReplyForwardRecord{ReplyKey: "k"}, 50, time.Now())
	assert.Error(t, err)
}

func TestVeridianReplyForwardRepo_LeaseDue_PromotesThenLeasesDueCopies(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(`UPDATE veridian_reply_forward SET status = 'failed', error_class = 'no_payload'[\s\S]*payload IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT DISTINCT profile_id FROM veridian_reply_forward WHERE status = 'deferred'`).
		WillReturnRows(sqlmock.NewRows([]string{"profile_id"}).AddRow("p1").AddRow("p2"))
	for _, expectedFree := range []int{4, 499} { // p1 a un plafond propre (5), p2 le defaut (500)
		mock.ExpectBegin()
		mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(`SELECT COUNT\(\*\)`).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
		mock.ExpectQuery(`UPDATE veridian_reply_forward SET status = 'retry'`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), expectedFree).
			WillReturnRows(sqlmock.NewRows([]string{"reply_key"}))
		mock.ExpectCommit()
	}
	mock.ExpectQuery(`UPDATE veridian_reply_forward SET status = 'pending'[\s\S]*status IN \('pending', 'retry'\) AND payload IS NOT NULL AND next_attempt_at <= \$1[\s\S]*FOR UPDATE SKIP LOCKED[\s\S]*RETURNING reply_key, profile_id, payload, attempts`).
		WithArgs(now, now.Add(domain.VeridianReplyForwardLease), 25).
		WillReturnRows(sqlmock.NewRows([]string{"reply_key", "profile_id", "payload", "attempts", "created_at"}).
			AddRow("k-late", "p1", "{}", 1, now.Add(-1*time.Minute)).
			AddRow("k1", "p1", "{}", 2, now.Add(-30*time.Minute)).
			AddRow("k-mid", "p1", "{}", 1, now.Add(-10*time.Minute)))
	jobs, err := repo.LeaseDue(context.Background(), "ws", map[string]int{"p1": 5}, 500, now, 25)
	require.NoError(t, err)
	require.Len(t, jobs, 3)
	assert.Equal(t, []string{"k1", "k-mid", "k-late"}, []string{jobs[0].ReplyKey, jobs[1].ReplyKey, jobs[2].ReplyKey}, "ordre d'arrivee des reponses, pas l'ordre de RETURNING")
	assert.Equal(t, 2, jobs[0].Attempts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Reschedule(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	next := time.Now().Add(time.Minute)
	mock.ExpectExec(`UPDATE veridian_reply_forward SET status = 'retry'`).WithArgs("k", "timeout", next).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Reschedule(context.Background(), "ws", "k", "timeout", next))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_ProfileStats(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	oldest := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`COUNT\(\*\) FILTER \(WHERE status = 'sent' AND sent_at >= \$1\)[\s\S]*GROUP BY profile_id`).
		WillReturnRows(sqlmock.NewRows([]string{"p", "sent", "pending", "retry", "deferred", "failed", "oldest"}).
			AddRow("prof1", 20, 1, 2, 3, 4, oldest).AddRow("prof2", 5, 0, 0, 0, 0, nil))
	mock.ExpectQuery(`SELECT profile_id, error_class, COUNT\(\*\) FROM veridian_reply_forward`).
		WillReturnRows(sqlmock.NewRows([]string{"p", "c", "n"}).AddRow("prof1", "smtp_4xx", 5).AddRow("prof1", "auth", 1))
	stats, err := repo.ProfileStats(context.Background(), "ws", time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, stats, 2)
	p1 := stats["prof1"]
	assert.Equal(t, 20, p1.Sent24h)
	assert.Equal(t, 1, p1.Pending)
	assert.Equal(t, 2, p1.Retry)
	assert.Equal(t, 3, p1.Deferred)
	assert.Equal(t, 4, p1.Failed7d)
	assert.Equal(t, "smtp_4xx", p1.DominantErrorClass, "la classe la plus frequente d'abord")
	require.NotNil(t, p1.OldestUnsentAt)
	assert.Equal(t, oldest, *p1.OldestUnsentAt)
	assert.Nil(t, stats["prof2"].OldestUnsentAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianReplyForwardRepo_Finish(t *testing.T) {
	repo, mock := newReplyForwardRepoMock(t)
	mock.ExpectExec(`UPDATE veridian_reply_forward SET status = \$2, error_class = NULLIF\(\$3, ''\), sent_at = \$4, payload = NULL`).WithArgs("k", "failed", "timeout", nil).
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
