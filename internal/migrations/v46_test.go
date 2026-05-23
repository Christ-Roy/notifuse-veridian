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

func TestV46Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 46.0, (&V46Migration{}).GetMajorVersion())
}

func TestV46Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V46Migration{}).HasSystemUpdate())
}

func TestV46Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V46Migration{}).HasWorkspaceUpdate())
}

func TestV46Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V46Migration{}).ShouldRestartServer())
}

func TestV46Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users\s+ADD COLUMN IF NOT EXISTS hub_user_id UUID NULL`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V46Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV46Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run : ALTER no-op grace a IF NOT EXISTS.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V46Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV46Migration_UpdateSystem_AlterError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnError(assert.AnError)

	err = (&V46Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add users.hub_user_id")
}

func TestV46Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V46Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV46Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 46.0 {
			return
		}
	}
	t.Fatal("V46Migration not registered")
}
