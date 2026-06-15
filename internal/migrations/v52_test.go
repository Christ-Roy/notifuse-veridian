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

func TestV52Migration_GetMajorVersion(t *testing.T) {
	migration := &V52Migration{}
	assert.Equal(t, 52.0, migration.GetMajorVersion())
}

func TestV52Migration_HasSystemUpdate(t *testing.T) {
	migration := &V52Migration{}
	assert.False(t, migration.HasSystemUpdate(), "V52 ne touche pas le schéma système")
}

func TestV52Migration_HasWorkspaceUpdate(t *testing.T) {
	migration := &V52Migration{}
	assert.True(t, migration.HasWorkspaceUpdate(), "V52 ajoute colonne+index sur message_history (table par workspace)")
}

func TestV52Migration_ShouldRestartServer(t *testing.T) {
	migration := &V52Migration{}
	assert.False(t, migration.ShouldRestartServer())
}

func TestV52Migration_UpdateSystem(t *testing.T) {
	migration := &V52Migration{}
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	err = migration.UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV52Migration_UpdateWorkspace(t *testing.T) {
	migration := &V52Migration{}
	ctx := context.Background()
	cfg := &config.Config{}
	workspace := &domain.Workspace{ID: "ws-test", Name: "Test"}

	t.Run("Success - ajoute la colonne puis l'index", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS veridian_content_hash").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_content_hash_sent_at").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Erreur sur ADD COLUMN est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS veridian_content_hash").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "veridian_content_hash")
	})

	t.Run("Erreur sur CREATE INDEX est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS veridian_content_hash").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_content_hash_sent_at").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "idx_message_history_content_hash_sent_at")
	})
}
