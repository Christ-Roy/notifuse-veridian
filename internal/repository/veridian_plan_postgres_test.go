package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockSystemDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestVeridianPlanRepository_Get(t *testing.T) {
	ctx := context.Background()
	const wsID = "ws-1"

	t.Run("found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		now := time.Now().UTC()
		rows := sqlmock.NewRows([]string{
			"workspace_id", "plan", "status", "monthly_email_quota", "emails_sent_this_month",
			"last_reset_at", "suspended_at", "suspended_reason", "deleted_at", "created_at", "updated_at",
		}).AddRow(wsID, "pro", "active", int64(10000), int64(42), now, nil, nil, nil, now, now)

		mock.ExpectQuery(`
			SELECT workspace_id, plan, status, monthly_email_quota, emails_sent_this_month,
			       last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
			FROM veridian_plan
			WHERE workspace_id = $1
		`).WithArgs(wsID).WillReturnRows(rows)

		p, err := repo.Get(ctx, wsID)
		require.NoError(t, err)
		assert.Equal(t, wsID, p.WorkspaceID)
		assert.Equal(t, "pro", p.Plan)
		assert.Equal(t, domain.PlanStatusActive, p.Status)
		assert.Equal(t, int64(10000), p.MonthlyEmailQuota)
		assert.Equal(t, int64(42), p.EmailsSentThisMonth)
		assert.Nil(t, p.SuspendedAt)
		assert.Nil(t, p.DeletedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("with suspended and deleted", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		now := time.Now().UTC()
		susp := now.Add(-1 * time.Hour)
		del := now.Add(-30 * time.Minute)
		rows := sqlmock.NewRows([]string{
			"workspace_id", "plan", "status", "monthly_email_quota", "emails_sent_this_month",
			"last_reset_at", "suspended_at", "suspended_reason", "deleted_at", "created_at", "updated_at",
		}).AddRow(wsID, "free", "suspended", int64(500), int64(0), now, susp, "non-payment", del, now, now)

		mock.ExpectQuery(`
			SELECT workspace_id, plan, status, monthly_email_quota, emails_sent_this_month,
			       last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
			FROM veridian_plan
			WHERE workspace_id = $1
		`).WithArgs(wsID).WillReturnRows(rows)

		p, err := repo.Get(ctx, wsID)
		require.NoError(t, err)
		assert.Equal(t, domain.PlanStatusSuspended, p.Status)
		require.NotNil(t, p.SuspendedAt)
		assert.Equal(t, "non-payment", p.SuspendedReason)
		require.NotNil(t, p.DeletedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectQuery(`
			SELECT workspace_id, plan, status, monthly_email_quota, emails_sent_this_month,
			       last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
			FROM veridian_plan
			WHERE workspace_id = $1
		`).WithArgs("missing").WillReturnError(sql.ErrNoRows)

		_, err := repo.Get(ctx, "missing")
		assert.ErrorIs(t, err, sql.ErrNoRows)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVeridianPlanRepository_Upsert(t *testing.T) {
	ctx := context.Background()

	t.Run("insert sets defaults", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		p := &domain.VeridianPlan{
			WorkspaceID:       "ws-new",
			Plan:              "free",
			MonthlyEmailQuota: 500,
		}

		mock.ExpectExec(`
			INSERT INTO veridian_plan (
				workspace_id, plan, status, monthly_email_quota, emails_sent_this_month,
				last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (workspace_id) DO UPDATE SET
				plan = EXCLUDED.plan,
				status = EXCLUDED.status,
				monthly_email_quota = EXCLUDED.monthly_email_quota,
				updated_at = EXCLUDED.updated_at
		`).WithArgs(
			"ws-new", "free", "active", int64(500), int64(0),
			sqlmock.AnyArg(), nil, "", nil, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Upsert(ctx, p)
		require.NoError(t, err)
		assert.Equal(t, domain.PlanStatusActive, p.Status, "status defaulted to active")
		assert.False(t, p.CreatedAt.IsZero())
		assert.False(t, p.UpdatedAt.IsZero())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rejects empty workspace_id", func(t *testing.T) {
		db, _ := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		err := repo.Upsert(ctx, &domain.VeridianPlan{Plan: "free"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "workspace_id required")
	})
}

func TestVeridianPlanRepository_UpdatePlan(t *testing.T) {
	ctx := context.Background()

	t.Run("updates existing", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET plan = $2, monthly_email_quota = $3, updated_at = $4
			WHERE workspace_id = $1
		`).WithArgs("ws-1", "pro", int64(10000), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdatePlan(ctx, "ws-1", "pro", 10000)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error if not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET plan = $2, monthly_email_quota = $3, updated_at = $4
			WHERE workspace_id = $1
		`).WithArgs("ws-missing", "pro", int64(10000), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdatePlan(ctx, "ws-missing", "pro", 10000)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestVeridianPlanRepository_Suspend(t *testing.T) {
	ctx := context.Background()

	t.Run("suspends existing", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET status = 'suspended', suspended_at = $2, suspended_reason = $3, updated_at = $2
			WHERE workspace_id = $1
		`).WithArgs("ws-1", sqlmock.AnyArg(), "non-payment").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Suspend(ctx, "ws-1", "non-payment")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error if not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET status = 'suspended', suspended_at = $2, suspended_reason = $3, updated_at = $2
			WHERE workspace_id = $1
		`).WithArgs("ws-missing", sqlmock.AnyArg(), "x").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Suspend(ctx, "ws-missing", "x")
		assert.ErrorContains(t, err, "not found")
	})
}

func TestVeridianPlanRepository_Resume(t *testing.T) {
	ctx := context.Background()

	t.Run("resumes existing", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET status = 'active', suspended_at = NULL, suspended_reason = NULL, updated_at = $2
			WHERE workspace_id = $1
		`).WithArgs("ws-1", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Resume(ctx, "ws-1")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error if not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET status = 'active', suspended_at = NULL, suspended_reason = NULL, updated_at = $2
			WHERE workspace_id = $1
		`).WithArgs("ws-missing", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Resume(ctx, "ws-missing")
		assert.ErrorContains(t, err, "not found")
	})
}

func TestVeridianPlanRepository_SoftDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("soft deletes existing", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET status = 'deleted', deleted_at = $2, updated_at = $2
			WHERE workspace_id = $1
		`).WithArgs("ws-1", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SoftDelete(ctx, "ws-1")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error if not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET status = 'deleted', deleted_at = $2, updated_at = $2
			WHERE workspace_id = $1
		`).WithArgs("ws-missing", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.SoftDelete(ctx, "ws-missing")
		assert.ErrorContains(t, err, "not found")
	})
}

func TestVeridianPlanRepository_IncrementEmailsSent(t *testing.T) {
	ctx := context.Background()

	t.Run("increments existing", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET emails_sent_this_month = emails_sent_this_month + $2, updated_at = $3
			WHERE workspace_id = $1
		`).WithArgs("ws-1", int64(5), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.IncrementEmailsSent(ctx, "ws-1", 5)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("noop if absent (no error)", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(`
			UPDATE veridian_plan
			SET emails_sent_this_month = emails_sent_this_month + $2, updated_at = $3
			WHERE workspace_id = $1
		`).WithArgs("ws-missing", int64(1), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.IncrementEmailsSent(ctx, "ws-missing", 1)
		require.NoError(t, err, "absent workspace should be silent no-op")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVeridianPlanRepository_ResetMonthlyCounters(t *testing.T) {
	ctx := context.Background()

	db, mock := newMockSystemDB(t)
	repo := NewVeridianPlanRepository(db)

	mock.ExpectExec(`
			UPDATE veridian_plan
			SET emails_sent_this_month = 0, last_reset_at = $1, updated_at = $1
			WHERE date_trunc('month', last_reset_at) < date_trunc('month', $1::timestamp)
		`).WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 7))

	n, err := repo.ResetMonthlyCounters(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}
