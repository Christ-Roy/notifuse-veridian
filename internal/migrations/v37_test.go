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

func TestV37Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 37.0, (&V37Migration{}).GetMajorVersion())
}

func TestV37Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V37Migration{}).HasSystemUpdate())
}

func TestV37Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V37Migration{}).HasWorkspaceUpdate())
}

func TestV37Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V37Migration{}).ShouldRestartServer())
}

// expectV37AddColumns enchaine les 9 ALTER TABLE ADD COLUMN IF NOT EXISTS.
// Helper pour ne pas dupliquer le boilerplate entre les cas success / error.
func expectV37AddColumns(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS max_contacts INTEGER NOT NULL DEFAULT 500`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS max_seats INTEGER NOT NULL DEFAULT 1`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS max_oauth_accounts INTEGER NOT NULL DEFAULT 1`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS max_custom_domains INTEGER NOT NULL DEFAULT 0`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS max_active_sequences INTEGER NOT NULL DEFAULT 1`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS feature_ab_testing BOOLEAN NOT NULL DEFAULT FALSE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS feature_branding_removed BOOLEAN NOT NULL DEFAULT FALSE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS feature_white_label BOOLEAN NOT NULL DEFAULT FALSE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS history_retention_days INTEGER NOT NULL DEFAULT 30`).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestV37Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV37AddColumns(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V37Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV37Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB ou les colonnes existent deja : IF NOT EXISTS no-op,
	// backfill re-applique (idempotent par construction CASE WHEN).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV37AddColumns(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V37Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV37Migration_UpdateSystem_AddColumnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS max_contacts`).
		WillReturnError(assert.AnError)

	err = (&V37Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add veridian_plan.max_contacts")
}

func TestV37Migration_UpdateSystem_BackfillError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV37AddColumns(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET`).
		WillReturnError(assert.AnError)

	err = (&V37Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill veridian_plan limits")
}

func TestV37Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V37Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}
