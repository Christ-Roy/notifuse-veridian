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

func TestV49Migration_GetMajorVersion(t *testing.T) {
	migration := &V49Migration{}
	assert.Equal(t, 49.0, migration.GetMajorVersion())
}

func TestV49Migration_HasSystemUpdate(t *testing.T) {
	migration := &V49Migration{}
	assert.False(t, migration.HasSystemUpdate(), "V49 ne touche pas le schéma système")
}

func TestV49Migration_HasWorkspaceUpdate(t *testing.T) {
	migration := &V49Migration{}
	assert.True(t, migration.HasWorkspaceUpdate(), "V49 ajoute des index sur message_history (table par workspace)")
}

func TestV49Migration_ShouldRestartServer(t *testing.T) {
	migration := &V49Migration{}
	assert.False(t, migration.ShouldRestartServer())
}

func TestV49Migration_UpdateSystem(t *testing.T) {
	migration := &V49Migration{}
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// No-op : aucune requête attendue.
	err = migration.UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV49Migration_UpdateWorkspace(t *testing.T) {
	migration := &V49Migration{}
	ctx := context.Background()
	cfg := &config.Config{}
	workspace := &domain.Workspace{ID: "ws-test", Name: "Test"}

	t.Run("Success - crée les deux index", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_contact_email_sent_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_sent_at").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("Erreur sur le 1er index est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_contact_email_sent_at").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "idx_message_history_contact_email_sent_at")
	})

	t.Run("Erreur sur le 2e index est propagée", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_contact_email_sent_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_sent_at").
			WillReturnError(errors.New("boom"))

		err = migration.UpdateWorkspace(ctx, cfg, workspace, db)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "idx_message_history_sent_at")
	})
}
