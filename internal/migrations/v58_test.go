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

func TestV58Migration_Flags(t *testing.T) {
	m := &V58Migration{}
	assert.Equal(t, 58.0, m.GetMajorVersion())
	assert.False(t, m.HasSystemUpdate())
	assert.True(t, m.HasWorkspaceUpdate())
	assert.False(t, m.ShouldRestartServer())
	require.NoError(t, m.UpdateSystem(context.Background(), &config.Config{}, nil))
}

func TestV58MigrationUpdateWorkspace(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("ALTER TABLE message_history ALTER COLUMN sent_at DROP NOT NULL").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE message_history SET sent_at = NULL WHERE sent_at IS NOT NULL AND failed_at IS NOT NULL").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("CREATE OR REPLACE FUNCTION webhook_message_history_trigger").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V58Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestV58MigrationUpdateWorkspace_RepairsFailedSentAt prouve le comportement du
// correctif de données embarqué dans la migration : une ligne historique portant
// à la fois sent_at et failed_at (signature exacte du bug du 28-29/09) doit voir
// sent_at repassé à NULL, jamais une ligne réellement envoyée (failed_at NULL).
func TestV58MigrationUpdateWorkspace_RepairsFailedSentAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("ALTER TABLE message_history ALTER COLUMN sent_at DROP NOT NULL").
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Le prédicat exact de réparation : sent_at IS NOT NULL AND failed_at IS NOT NULL.
	mock.ExpectExec(`UPDATE message_history SET sent_at = NULL WHERE sent_at IS NOT NULL AND failed_at IS NOT NULL`).
		WillReturnResult(sqlmock.NewResult(0, 36)) // les 36 lignes de l'incident robertbrunon
	mock.ExpectExec("CREATE OR REPLACE FUNCTION webhook_message_history_trigger").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V58Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestV58MigrationUpdateWorkspace_AlterColumnErrorPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("ALTER TABLE message_history ALTER COLUMN sent_at DROP NOT NULL").
		WillReturnError(errors.New("lock timeout"))

	err = (&V58Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nullable")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestV58MigrationUpdateWorkspace_RepairUpdateErrorPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("ALTER TABLE message_history ALTER COLUMN sent_at DROP NOT NULL").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE message_history SET sent_at = NULL WHERE sent_at IS NOT NULL AND failed_at IS NOT NULL").
		WillReturnError(errors.New("boom"))

	err = (&V58Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repair message_history rows")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestV58MigrationUpdateWorkspace_TriggerErrorPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec("ALTER TABLE message_history ALTER COLUMN sent_at DROP NOT NULL").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE message_history SET sent_at = NULL WHERE sent_at IS NOT NULL AND failed_at IS NOT NULL").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE OR REPLACE FUNCTION webhook_message_history_trigger").
		WillReturnError(errors.New("syntax error"))

	err = (&V58Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook_message_history_trigger")
	require.NoError(t, mock.ExpectationsWereMet())
}
