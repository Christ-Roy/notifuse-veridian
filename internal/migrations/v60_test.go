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

func TestV60Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 60.0, (&V60Migration{}).GetMajorVersion())
}

func TestV60Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V60Migration{}).HasSystemUpdate())
}

func TestV60Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V60Migration{}).HasWorkspaceUpdate())
}

func TestV60Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V60Migration{}).ShouldRestartServer())
}

func TestV60Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_idempotency_tenant_key`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V60Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV60Migration_UpdateSystem_Idempotent(t *testing.T) {
	// EXPAND phase additive (IF NOT EXISTS) : rejouer la migration ne doit
	// jamais echouer meme si l'index existe deja.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_idempotency_tenant_key`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V60Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV60Migration_UpdateSystem_CreateIndexError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_idempotency_tenant_key`).
		WillReturnError(assert.AnError)

	err = (&V60Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idx_veridian_idempotency_tenant_key")
}

func TestV60Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V60Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV60Migration_RegisteredInRegistry(t *testing.T) {
	found := false
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 60.0 {
			found = true
			break
		}
	}
	assert.True(t, found, "V60Migration doit être enregistrée dans le registry")
}
