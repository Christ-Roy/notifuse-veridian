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

func TestV39Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 39.0, (&V39Migration{}).GetMajorVersion())
}

func TestV39Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V39Migration{}).HasSystemUpdate())
}

func TestV39Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V39Migration{}).HasWorkspaceUpdate())
}

func TestV39Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V39Migration{}).ShouldRestartServer())
}

// expectV39AddColumn enchaine le ALTER TABLE ADD COLUMN IF NOT EXISTS.
func expectV39AddColumn(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS last_hub_sync_at TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestV39Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV39AddColumn(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET last_hub_sync_at = updated_at AT TIME ZONE 'UTC'`).
		WillReturnResult(sqlmock.NewResult(0, 3))

	err = (&V39Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV39Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB où la colonne existe déjà et le backfill a déjà été fait
	// (last_hub_sync_at IS NOT NULL sur toutes les rows).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV39AddColumn(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET last_hub_sync_at = updated_at AT TIME ZONE 'UTC'`).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows updated = déjà backfillé

	err = (&V39Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV39Migration_UpdateSystem_AddColumnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS last_hub_sync_at`).
		WillReturnError(assert.AnError)

	err = (&V39Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add veridian_plan.last_hub_sync_at")
}

func TestV39Migration_UpdateSystem_BackfillError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV39AddColumn(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET last_hub_sync_at = updated_at AT TIME ZONE 'UTC'`).
		WillReturnError(assert.AnError)

	err = (&V39Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill veridian_plan last_hub_sync_at")
}

func TestV39Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V39Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV39Migration_RegisteredInRegistry(t *testing.T) {
	// Vérifie que la migration est bien enregistrée (le init() a été exécuté).
	found := false
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 39.0 {
			found = true
			break
		}
	}
	assert.True(t, found, "V39Migration doit être enregistrée dans le registry")
}
