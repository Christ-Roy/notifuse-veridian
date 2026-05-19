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

func TestV35Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 35.0, (&V35Migration{}).GetMajorVersion())
}

func TestV35Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V35Migration{}).HasSystemUpdate())
}

func TestV35Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V35Migration{}).HasWorkspaceUpdate())
}

func TestV35Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V35Migration{}).ShouldRestartServer())
}

func TestV35Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_idempotency_keys`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V35Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV35Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB ou la table existe deja : IF NOT EXISTS no-op.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_idempotency_keys`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V35Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV35Migration_UpdateSystem_CreateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_idempotency_keys`).
		WillReturnError(assert.AnError)

	err = (&V35Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create veridian_idempotency_keys")
}

func TestV35Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V35Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}
