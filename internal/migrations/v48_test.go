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

func TestV48Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 48.0, (&V48Migration{}).GetMajorVersion())
}

func TestV48Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V48Migration{}).HasSystemUpdate())
}

func TestV48Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V48Migration{}).HasWorkspaceUpdate())
}

func TestV48Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V48Migration{}).ShouldRestartServer())
}

func TestV48Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE workspaces\s+ADD COLUMN IF NOT EXISTS mail_provider_choice`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V48Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV48Migration_UpdateSystem_Idempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Re-run = ADD COLUMN IF NOT EXISTS retourne 0 row affected sans erreur.
	mock.ExpectExec(`ALTER TABLE workspaces\s+ADD COLUMN IF NOT EXISTS mail_provider_choice`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V48Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV48Migration_UpdateSystem_AlterError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE workspaces\s+ADD COLUMN IF NOT EXISTS mail_provider_choice`).
		WillReturnError(assert.AnError)

	err = (&V48Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add workspaces.mail_provider_choice")
}

func TestV48Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V48Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV48Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 48.0 {
			return
		}
	}
	t.Fatal("V48Migration not registered")
}
