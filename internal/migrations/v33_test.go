package migrations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

func TestV33Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 33.0, (&V33Migration{}).GetMajorVersion())
}

func TestV33Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V33Migration{}).HasSystemUpdate())
}

func TestV33Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V33Migration{}).HasWorkspaceUpdate())
}

func TestV33Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V33Migration{}).ShouldRestartServer())
}

func TestV33Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS plan_source VARCHAR\(32\) NOT NULL DEFAULT 'stripe'`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_plan_source ON veridian_plan \(plan_source\)\s+WHERE plan_source <> 'stripe'`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V33Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV33Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB ou la colonne et l'index existent deja : ALTER + CREATE
	// sont no-op (IF NOT EXISTS).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_plan_source`).WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V33Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV33Migration_UpdateSystem_AlterError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan`).WillReturnError(assert.AnError)

	err = (&V33Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add veridian_plan.plan_source")
}

func TestV33Migration_UpdateSystem_IndexError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_plan_source`).WillReturnError(assert.AnError)

	err = (&V33Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create idx_veridian_plan_source")
}

func TestV33Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V33Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}
