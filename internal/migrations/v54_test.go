package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV54Migration_GetMajorVersion(t *testing.T) {
	migration := &V54Migration{}
	assert.Equal(t, 54.0, migration.GetMajorVersion())
}

func TestV54Migration_HasSystemUpdate(t *testing.T) {
	migration := &V54Migration{}
	assert.False(t, migration.HasSystemUpdate(), "V54 ne touche pas le schéma système")
}

func TestV54Migration_HasWorkspaceUpdate(t *testing.T) {
	migration := &V54Migration{}
	assert.True(t, migration.HasWorkspaceUpdate(), "V54 ajoute bounce_type sur message_history (table par workspace)")
}

func TestV54Migration_ShouldRestartServer(t *testing.T) {
	migration := &V54Migration{}
	assert.False(t, migration.ShouldRestartServer())
}

func TestV54Migration_UpdateSystem(t *testing.T) {
	migration := &V54Migration{}
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	err = migration.UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV54Migration_UpdateWorkspace(t *testing.T) {
	migration := &V54Migration{}
	ctx := context.Background()
	cfg := &config.Config{}
	workspace := &domain.Workspace{ID: "ws-test", Name: "Test"}

	t.Run("Success - ajoute la colonne bounce_type (idempotent)", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS bounce_type VARCHAR\\(100\\)").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Erreur sur ADD COLUMN est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS bounce_type").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "bounce_type")
	})
}
