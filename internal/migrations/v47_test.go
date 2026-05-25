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

func TestV47Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 47.0, (&V47Migration{}).GetMajorVersion())
}

func TestV47Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V47Migration{}).HasSystemUpdate())
}

func TestV47Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V47Migration{}).HasWorkspaceUpdate())
}

func TestV47Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V47Migration{}).ShouldRestartServer())
}

func TestV47Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_frozen_members`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V47Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV47Migration_UpdateSystem_Idempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_frozen_members`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V47Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV47Migration_UpdateSystem_CreateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_frozen_members`).
		WillReturnError(assert.AnError)

	err = (&V47Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create veridian_frozen_members")
}

func TestV47Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V47Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV47Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 47.0 {
			return
		}
	}
	t.Fatal("V47Migration not registered")
}
