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

func TestV51Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 51.0, (&V51Migration{}).GetMajorVersion())
}

func TestV51Migration_HasSystemUpdate(t *testing.T) {
	// Table veridian_contact_reply = donnée métier-contact → par WORKSPACE.
	assert.False(t, (&V51Migration{}).HasSystemUpdate())
}

func TestV51Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.True(t, (&V51Migration{}).HasWorkspaceUpdate())
}

func TestV51Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V51Migration{}).ShouldRestartServer())
}

func TestV51Migration_UpdateWorkspace_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_contact_reply`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V51Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV51Migration_UpdateWorkspace_Idempotent(t *testing.T) {
	// Re-run (rollback/redeploy) : IF NOT EXISTS => même exec sans erreur.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_contact_reply`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V51Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV51Migration_UpdateWorkspace_CreateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_contact_reply`).
		WillReturnError(assert.AnError)

	err = (&V51Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create veridian_contact_reply")
}

func TestV51Migration_UpdateSystem_Noop(t *testing.T) {
	// Migration workspace : UpdateSystem ne lance AUCUNE requête.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V51Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV51Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 51.0 {
			return
		}
	}
	t.Fatal("V51Migration not registered")
}
