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
			"workspace_id", "plan", "plan_source", "status", "monthly_email_quota", "emails_sent_this_month",
			"last_reset_at", "suspended_at", "suspended_reason", "deleted_at", "created_at", "updated_at",
		}).AddRow(wsID, "pro", "stripe", "active", int64(10000), int64(42), now, nil, nil, nil, now, now)

		mock.ExpectQuery(`
			SELECT workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
			       last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
			FROM veridian_plan
			WHERE workspace_id = $1
		`).WithArgs(wsID).WillReturnRows(rows)

		p, err := repo.Get(ctx, wsID)
		require.NoError(t, err)
		assert.Equal(t, wsID, p.WorkspaceID)
		assert.Equal(t, "pro", p.Plan)
		assert.Equal(t, domain.PlanSourceStripe, p.PlanSource)
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
			"workspace_id", "plan", "plan_source", "status", "monthly_email_quota", "emails_sent_this_month",
			"last_reset_at", "suspended_at", "suspended_reason", "deleted_at", "created_at", "updated_at",
		}).AddRow(wsID, "free", "lifetime_partner", "suspended", int64(500), int64(0), now, susp, "non-payment", del, now, now)

		mock.ExpectQuery(`
			SELECT workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
			       last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
			FROM veridian_plan
			WHERE workspace_id = $1
		`).WithArgs(wsID).WillReturnRows(rows)

		p, err := repo.Get(ctx, wsID)
		require.NoError(t, err)
		assert.Equal(t, domain.PlanStatusSuspended, p.Status)
		assert.Equal(t, domain.PlanSourceLifetimePartner, p.PlanSource, "plan_source must be scanned from DB")
		require.NotNil(t, p.SuspendedAt)
		assert.Equal(t, "non-payment", p.SuspendedReason)
		require.NotNil(t, p.DeletedAt)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectQuery(`
			SELECT workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
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

	const upsertSQL = `
		INSERT INTO veridian_plan (
			workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
			last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
		) VALUES ($1,$2,COALESCE($3,'stripe'),$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (workspace_id) DO UPDATE SET
			plan = EXCLUDED.plan,
			plan_source = COALESCE(EXCLUDED.plan_source, veridian_plan.plan_source),
			status = EXCLUDED.status,
			monthly_email_quota = EXCLUDED.monthly_email_quota,
			updated_at = EXCLUDED.updated_at
	`

	t.Run("insert sets defaults (plan_source vide → nil → 'stripe' via COALESCE)", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		p := &domain.VeridianPlan{
			WorkspaceID:       "ws-new",
			Plan:              "free",
			MonthlyEmailQuota: 500,
		}

		mock.ExpectExec(upsertSQL).WithArgs(
			"ws-new", "free", nil, "active", int64(500), int64(0),
			sqlmock.AnyArg(), nil, "", nil, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Upsert(ctx, p)
		require.NoError(t, err)
		assert.Equal(t, domain.PlanStatusActive, p.Status, "status defaulted to active")
		assert.False(t, p.CreatedAt.IsZero())
		assert.False(t, p.UpdatedAt.IsZero())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert with explicit plan_source persists it", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		p := &domain.VeridianPlan{
			WorkspaceID:       "ws-vip",
			Plan:              "enterprise",
			PlanSource:        domain.PlanSourceLifetimePartner,
			MonthlyEmailQuota: -1,
		}

		mock.ExpectExec(upsertSQL).WithArgs(
			"ws-vip", "enterprise", "lifetime_partner", "active", int64(-1), int64(0),
			sqlmock.AnyArg(), nil, "", nil, sqlmock.AnyArg(), sqlmock.AnyArg(),
		).WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Upsert(ctx, p)
		require.NoError(t, err)
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

	// QueryMatcherEqual exige match exact (espaces compris). On reprend le
	// SQL litteral du repo plutot que d'essayer de l'aligner manuellement.
	const expectedSQL = `
		UPDATE veridian_plan
		SET plan = $2,
		    monthly_email_quota = $3,
		    plan_source = COALESCE($4, plan_source),
		    updated_at = $5
		WHERE workspace_id = $1
	`

	t.Run("updates existing with explicit plan_source", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(expectedSQL).
			WithArgs("ws-1", "pro", int64(10000), "lifetime_partner", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdatePlan(ctx, "ws-1", "pro", 10000, domain.PlanSourceLifetimePartner)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty plan_source passes nil (COALESCE preserves existing)", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		// Quand l'appelant passe "" (rétro-compat Hub legacy), le repo passe NULL
		// pour que le COALESCE preserve la valeur DB existante.
		mock.ExpectExec(expectedSQL).
			WithArgs("ws-1", "pro", int64(10000), nil, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdatePlan(ctx, "ws-1", "pro", 10000, domain.PlanSource(""))
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("returns error if not found", func(t *testing.T) {
		db, mock := newMockSystemDB(t)
		repo := NewVeridianPlanRepository(db)

		mock.ExpectExec(expectedSQL).
			WithArgs("ws-missing", "pro", int64(10000), nil, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdatePlan(ctx, "ws-missing", "pro", 10000, domain.PlanSource(""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

// TestVeridianPlanRepository_UpdatePlan_PreservesSourceOnEmpty verifie le
// comportement COALESCE introduit pour le sec. 3.3 plan_source : un appel
// UpdatePlan avec planSource="" passe NIL en SQL pour preserver la valeur
// DB existante (vs ecraser par 'stripe'). Ce test est dedie top-level pour
// la regle Constitution §1 1-pour-1 sur la signature etendue.
func TestVeridianPlanRepository_UpdatePlan_PreservesSourceOnEmpty(t *testing.T) {
	ctx := context.Background()
	db, mock := newMockSystemDB(t)
	repo := NewVeridianPlanRepository(db)

	// Le 4eme arg DOIT etre nil (pas la string vide) pour que le COALESCE
	// SQL preserve la valeur existante en DB. C'est le comportement attendu
	// quand le Hub legacy appelle update-plan sans envoyer plan_source.
	const sqlPattern = `
		UPDATE veridian_plan
		SET plan = $2,
		    monthly_email_quota = $3,
		    plan_source = COALESCE($4, plan_source),
		    updated_at = $5
		WHERE workspace_id = $1
	`
	mock.ExpectExec(sqlPattern).
		WithArgs("ws-vip-untouched", "enterprise", int64(-1), nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdatePlan(ctx, "ws-vip-untouched", "enterprise", -1, domain.PlanSource(""))
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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
