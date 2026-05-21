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

func TestV40Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 40.0, (&V40Migration{}).GetMajorVersion())
}

func TestV40Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V40Migration{}).HasSystemUpdate())
}

func TestV40Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V40Migration{}).HasWorkspaceUpdate())
}

func TestV40Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V40Migration{}).ShouldRestartServer())
}

func TestV40Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS quota_exceeded_emitted_at_month TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V40Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV40Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB où la colonne existe déjà : ADD COLUMN IF NOT EXISTS no-op.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS quota_exceeded_emitted_at_month TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V40Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV40Migration_UpdateSystem_AddColumnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS quota_exceeded_emitted_at_month`).
		WillReturnError(assert.AnError)

	err = (&V40Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add veridian_plan.quota_exceeded_emitted_at_month")
}

func TestV40Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V40Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV40Migration_RegisteredInRegistry(t *testing.T) {
	found := false
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 40.0 {
			found = true
			break
		}
	}
	assert.True(t, found, "V40Migration doit être enregistrée dans le registry")
}
