package migrations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV57MigrationContract(t *testing.T) {
	migration := &V57Migration{}
	assert.Equal(t, 57.0, migration.GetMajorVersion())
	assert.True(t, migration.HasSystemUpdate())
	assert.False(t, migration.HasWorkspaceUpdate())
	assert.False(t, migration.ShouldRestartServer())
	assert.NoError(t, migration.UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, nil))

	found := false
	for _, registered := range GetRegisteredMigrations() {
		found = found || registered.GetMajorVersion() == 57.0
	}
	assert.True(t, found)
}

func TestV57MigrationCreatesSafetyTablesAndIndexes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_global_suppressions").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("(?s)CREATE TABLE IF NOT EXISTS veridian_send_reservations.*UNIQUE \\(workspace_id, queue_entry_id, attempt, quota_date\\)").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_veridian_send_reservations_workspace_date").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_veridian_send_reservations_provider_rate").WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, (&V57Migration{}).UpdateSystem(context.Background(), &config.Config{}, db))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestV57MigrationFailsOnDDLFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_global_suppressions").WillReturnError(assert.AnError)
	err = (&V57Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.ErrorContains(t, err, "global suppressions")
}
