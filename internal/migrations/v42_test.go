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

func TestV42Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 42.0, (&V42Migration{}).GetMajorVersion())
}

func TestV42Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V42Migration{}).HasSystemUpdate())
}

func TestV42Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V42Migration{}).HasWorkspaceUpdate())
}

func TestV42Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V42Migration{}).ShouldRestartServer())
}

func TestV42Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users\s+ADD COLUMN IF NOT EXISTS language VARCHAR\(10\) NOT NULL DEFAULT 'en'`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V42Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV42Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run après backfill : ALTER no-op grâce à IF NOT EXISTS.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V42Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV42Migration_UpdateSystem_AlterError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnError(assert.AnError)

	err = (&V42Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add users.language")
}

func TestV42Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V42Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV42Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 42.0 {
			return
		}
	}
	t.Fatal("V42Migration not registered")
}
