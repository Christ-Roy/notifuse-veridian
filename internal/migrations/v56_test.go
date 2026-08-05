package migrations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestV56MigrationUpdateWorkspace(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("ALTER TABLE message_history ADD COLUMN IF NOT EXISTS veridian_profile_id").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_profile_sent_at").WillReturnResult(sqlmock.NewResult(0, 0))
	err = (&V56Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{}, db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
