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

func TestV32Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 32.0, (&V32Migration{}).GetMajorVersion())
}

func TestV32Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V32Migration{}).HasSystemUpdate())
}

func TestV32Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V32Migration{}).HasWorkspaceUpdate())
}

func TestV32Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V32Migration{}).ShouldRestartServer())
}

func TestV32Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users\s+ADD COLUMN IF NOT EXISTS veridian_managed BOOLEAN NOT NULL DEFAULT FALSE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE users\s+SET veridian_managed = TRUE\s+WHERE type = 'api_key'\s+AND email LIKE 'veridian-api-%@notifuse\.%'`).
		WillReturnResult(sqlmock.NewResult(0, 3))

	err = (&V32Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV32Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run après backfill : ALTER no-op (IF NOT EXISTS) + UPDATE filtre
	// veridian_managed = FALSE → 0 rows. Aucune erreur.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE users`).WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V32Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV32Migration_UpdateSystem_AlterError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnError(assert.AnError)

	err = (&V32Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add users.veridian_managed")
}

func TestV32Migration_UpdateSystem_BackfillError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE users`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE users`).WillReturnError(assert.AnError)

	err = (&V32Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill veridian_managed")
}

func TestV32Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V32Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}
