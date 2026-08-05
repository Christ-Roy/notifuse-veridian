package migrations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestV55Migration(t *testing.T) {
	migration := &V55Migration{}
	require.Equal(t, 55.0, migration.GetMajorVersion())
	require.False(t, migration.HasSystemUpdate())
	require.True(t, migration.HasWorkspaceUpdate())
	require.False(t, migration.ShouldRestartServer())

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec("ALTER TABLE message_history").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_daily_quota_counters").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS veridian_daily_quota_reservations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_message_history_provider_class_sender_sent_at").WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, migration.UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db))
	require.NoError(t, mock.ExpectationsWereMet())
}
