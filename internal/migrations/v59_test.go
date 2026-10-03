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

func TestV59Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 59.0, (&V59Migration{}).GetMajorVersion())
}

func TestV59Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V59Migration{}).HasSystemUpdate())
}

func TestV59Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V59Migration{}).HasWorkspaceUpdate())
}

func TestV59Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V59Migration{}).ShouldRestartServer())
}

func TestV59Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS agent_install_tokens`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V59Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV59Migration_UpdateSystem_Idempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS agent_install_tokens`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V59Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV59Migration_UpdateSystem_CreateTableError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS agent_install_tokens`).
		WillReturnError(assert.AnError)

	err = (&V59Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create agent_install_tokens")
}

func TestV59Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V59Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV59Migration_RegisteredInRegistry(t *testing.T) {
	found := false
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 59.0 {
			found = true
			break
		}
	}
	assert.True(t, found, "V59Migration doit être enregistrée dans le registry")
}
