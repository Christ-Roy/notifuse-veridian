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

const freezeInsertSQL = `
		INSERT INTO veridian_frozen_members (workspace_id, user_id, reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO NOTHING
		RETURNING workspace_id, user_id, frozen_at, reason
	`

const freezeSelectSQL = `
		SELECT workspace_id, user_id, frozen_at, reason
		FROM veridian_frozen_members
		WHERE workspace_id = $1 AND user_id = $2
	`

const freezeDeleteSQL = `DELETE FROM veridian_frozen_members WHERE workspace_id = $1 AND user_id = $2`

const freezeIsFrozenSQL = `SELECT reason FROM veridian_frozen_members WHERE workspace_id = $1 AND user_id = $2`

func TestNewVeridianFrozenMemberRepository_Constructor(t *testing.T) {
	db, _ := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)
	require.NotNil(t, repo)
	var _ domain.VeridianFrozenMemberRepository = repo
}

func TestVeridianFrozenMemberRepository_Freeze_NewRow(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"workspace_id", "user_id", "frozen_at", "reason"}).
		AddRow("ws-1", "u-1", now, "quota_seat_exceeded")
	mock.ExpectQuery(freezeInsertSQL).
		WithArgs("ws-1", "u-1", "quota_seat_exceeded").
		WillReturnRows(rows)

	fm, alreadyFrozen, err := repo.Freeze(ctx, "ws-1", "u-1", domain.FreezeReasonQuotaSeatExceeded)
	require.NoError(t, err)
	assert.False(t, alreadyFrozen)
	assert.Equal(t, "ws-1", fm.WorkspaceID)
	assert.Equal(t, "u-1", fm.UserID)
	assert.Equal(t, domain.FreezeReasonQuotaSeatExceeded, fm.Reason)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_Freeze_AlreadyExists(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	originalTime := time.Now().UTC().Add(-1 * time.Hour)

	// INSERT ... DO NOTHING : 0 rows returned (conflit silencieux).
	mock.ExpectQuery(freezeInsertSQL).
		WithArgs("ws-1", "u-1", "manual").
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "user_id", "frozen_at", "reason"}))

	// Fallback SELECT recupere la row existante.
	mock.ExpectQuery(freezeSelectSQL).
		WithArgs("ws-1", "u-1").
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "user_id", "frozen_at", "reason"}).
			AddRow("ws-1", "u-1", originalTime, "quota_seat_exceeded"))

	fm, alreadyFrozen, err := repo.Freeze(ctx, "ws-1", "u-1", domain.FreezeReasonManual)
	require.NoError(t, err)
	assert.True(t, alreadyFrozen, "second freeze on same (ws, user) must return alreadyFrozen=true")
	assert.Equal(t, "quota_seat_exceeded", string(fm.Reason), "original reason preserved, not overwritten")
	assert.WithinDuration(t, originalTime, fm.FrozenAt, time.Second)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_Freeze_DefaultReason_Manual(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	// Reason vide → repo defensive → fallback "manual" envoyé au SQL.
	mock.ExpectQuery(freezeInsertSQL).
		WithArgs("ws-1", "u-1", "manual").
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "user_id", "frozen_at", "reason"}).
			AddRow("ws-1", "u-1", time.Now(), "manual"))

	_, _, err := repo.Freeze(ctx, "ws-1", "u-1", "")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_Freeze_Validation(t *testing.T) {
	ctx := context.Background()
	db, _ := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	_, _, err := repo.Freeze(ctx, "", "u-1", domain.FreezeReasonManual)
	require.Error(t, err)
	_, _, err = repo.Freeze(ctx, "ws-1", "", domain.FreezeReasonManual)
	require.Error(t, err)
}

func TestVeridianFrozenMemberRepository_Unfreeze_WasFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	mock.ExpectExec(freezeDeleteSQL).
		WithArgs("ws-1", "u-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	wasFrozen, err := repo.Unfreeze(ctx, "ws-1", "u-1")
	require.NoError(t, err)
	assert.True(t, wasFrozen, "DELETE 1 row → wasFrozen=true")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_Unfreeze_Idempotent_NotFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	// DELETE 0 rows = pas frozen.
	mock.ExpectExec(freezeDeleteSQL).
		WithArgs("ws-1", "u-1").
		WillReturnResult(sqlmock.NewResult(0, 0))

	wasFrozen, err := repo.Unfreeze(ctx, "ws-1", "u-1")
	require.NoError(t, err)
	assert.False(t, wasFrozen, "DELETE 0 rows → wasFrozen=false")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_Unfreeze_DBError(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	mock.ExpectExec(freezeDeleteSQL).
		WithArgs("ws-1", "u-1").
		WillReturnError(errors.New("conn dead"))

	wasFrozen, err := repo.Unfreeze(ctx, "ws-1", "u-1")
	require.Error(t, err)
	assert.False(t, wasFrozen)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_Unfreeze_Validation(t *testing.T) {
	ctx := context.Background()
	db, _ := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	_, err := repo.Unfreeze(ctx, "", "u-1")
	require.Error(t, err)
	_, err = repo.Unfreeze(ctx, "ws-1", "")
	require.Error(t, err)
}

func TestVeridianFrozenMemberRepository_IsFrozen_True(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	mock.ExpectQuery(freezeIsFrozenSQL).
		WithArgs("ws-1", "u-1").
		WillReturnRows(sqlmock.NewRows([]string{"reason"}).AddRow("quota_seat_exceeded"))

	frozen, reason, err := repo.IsFrozen(ctx, "ws-1", "u-1")
	require.NoError(t, err)
	assert.True(t, frozen)
	assert.Equal(t, domain.FreezeReasonQuotaSeatExceeded, reason)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_IsFrozen_False(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	mock.ExpectQuery(freezeIsFrozenSQL).
		WithArgs("ws-1", "u-1").
		WillReturnRows(sqlmock.NewRows([]string{"reason"})) // 0 rows

	frozen, reason, err := repo.IsFrozen(ctx, "ws-1", "u-1")
	require.NoError(t, err)
	assert.False(t, frozen)
	assert.Empty(t, string(reason))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_IsFrozen_DBError(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	mock.ExpectQuery(freezeIsFrozenSQL).
		WithArgs("ws-1", "u-1").
		WillReturnError(errors.New("conn refused"))

	frozen, _, err := repo.IsFrozen(ctx, "ws-1", "u-1")
	require.Error(t, err)
	assert.False(t, frozen, "DB error propagated, frozen defaults to false (middleware fail-open)")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianFrozenMemberRepository_IsFrozen_EmptyInputs_Skip(t *testing.T) {
	ctx := context.Background()
	db, _ := newMockSystemDB(t)
	repo := NewVeridianFrozenMemberRepository(db)

	// Empty workspace_id or user_id : skip DB roundtrip, return (false, nil)
	// — defensive against middleware paths qui n'arrivent pas a resoudre l'IDs.
	frozen, _, err := repo.IsFrozen(ctx, "", "u-1")
	require.NoError(t, err)
	assert.False(t, frozen)

	frozen, _, err = repo.IsFrozen(ctx, "ws-1", "")
	require.NoError(t, err)
	assert.False(t, frozen)
}
