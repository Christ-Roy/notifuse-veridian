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

func TestV41Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 41.0, (&V41Migration{}).GetMajorVersion())
}

func TestV41Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V41Migration{}).HasSystemUpdate())
}

func TestV41Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V41Migration{}).HasWorkspaceUpdate())
}

func TestV41Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V41Migration{}).ShouldRestartServer())
}

func TestV41Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_api_key_grace`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V41Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV41Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB où la table existe déjà — CREATE TABLE IF NOT EXISTS = no-op.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_api_key_grace`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V41Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV41Migration_UpdateSystem_CreateTableError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_api_key_grace`).
		WillReturnError(assert.AnError)

	err = (&V41Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create veridian_api_key_grace")
}

func TestV41Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V41Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV41Migration_RegisteredInRegistry(t *testing.T) {
	found := false
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 41.0 {
			found = true
			break
		}
	}
	assert.True(t, found, "V41Migration doit être enregistrée dans le registry")
}
