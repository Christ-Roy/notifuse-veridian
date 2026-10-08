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

func TestV61Migration_Metadata(t *testing.T) {
	m := &V61Migration{}
	assert.Equal(t, 61.0, m.GetMajorVersion())
	assert.False(t, m.HasSystemUpdate())
	assert.True(t, m.HasWorkspaceUpdate())
	assert.False(t, m.ShouldRestartServer())
}

func TestV61Migration_UpdateSystem_NoOp(t *testing.T) {
	assert.NoError(t, (&V61Migration{}).UpdateSystem(context.Background(), &config.Config{}, nil))
}

func TestV61Migration_UpdateWorkspace_AddsMessageType(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE message_history ADD COLUMN IF NOT EXISTS veridian_message_type VARCHAR\(16\)`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V61Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV61Migration_UpdateWorkspace_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE message_history`).WillReturnError(errors.New("boom"))

	err = (&V61Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "veridian_message_type")
}
