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

func TestV23MigrationContract(t *testing.T) {
	migration := &V23Migration{}
	assert.Equal(t, 23.0, migration.GetMajorVersion())
	assert.True(t, migration.HasSystemUpdate())
	assert.False(t, migration.HasWorkspaceUpdate())
	assert.False(t, migration.ShouldRestartServer())
	assert.NoError(t, migration.UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, nil))

	found := false
	for _, registered := range GetRegisteredMigrations() {
		found = found || registered.GetMajorVersion() == 23.0
	}
	assert.True(t, found)
}

func TestV23MigrationCreatesSafetyTablesAndIndexes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_global_suppressions").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_send_reservations.*queue_entry_id").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_veridian_send_reservations_workspace_date").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_veridian_send_reservations_provider_rate").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V23Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestV23MigrationFailsClosedOnDDLFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_global_suppressions").
		WillReturnError(assert.AnError)
	err = (&V23Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.ErrorContains(t, err, "global suppressions")
}
