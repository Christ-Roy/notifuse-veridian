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

func TestV53Migration_GetMajorVersion(t *testing.T) {
	migration := &V53Migration{}
	assert.Equal(t, 53.0, migration.GetMajorVersion())
}

func TestV53Migration_HasSystemUpdate(t *testing.T) {
	migration := &V53Migration{}
	assert.False(t, migration.HasSystemUpdate(), "V53 ne touche pas le schéma système")
}

func TestV53Migration_HasWorkspaceUpdate(t *testing.T) {
	migration := &V53Migration{}
	assert.True(t, migration.HasWorkspaceUpdate(), "V53 ajoute colonne+index sur message_history (table par workspace)")
}

func TestV53Migration_ShouldRestartServer(t *testing.T) {
	migration := &V53Migration{}
	assert.False(t, migration.ShouldRestartServer())
}

func TestV53Migration_UpdateSystem(t *testing.T) {
	migration := &V53Migration{}
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	err = migration.UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV53Migration_UpdateWorkspace(t *testing.T) {
	migration := &V53Migration{}
	ctx := context.Background()
	cfg := &config.Config{}
	workspace := &domain.Workspace{ID: "ws-test", Name: "Test"}

	t.Run("Success - ajoute la colonne veridian_sender_email puis l'index", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS veridian_sender_email").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_sender_email_sent_at").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Erreur sur ADD COLUMN est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS veridian_sender_email").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "veridian_sender_email")
	})

	t.Run("Erreur sur CREATE INDEX est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("ALTER TABLE message_history\\s+ADD COLUMN IF NOT EXISTS veridian_sender_email").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_sender_email_sent_at").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "idx_message_history_sender_email_sent_at")
	})
}
