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

func TestV60Migration_Metadata(t *testing.T) {
	m := &V60Migration{}
	assert.Equal(t, 60.0, m.GetMajorVersion())
	assert.False(t, m.HasSystemUpdate())
	assert.True(t, m.HasWorkspaceUpdate())
	assert.False(t, m.ShouldRestartServer())
}

func TestV60Migration_UpdateSystem_NoOp(t *testing.T) {
	assert.NoError(t, (&V60Migration{}).UpdateSystem(context.Background(), &config.Config{}, nil))
}

func TestV60Migration_UpdateWorkspace_AddsReplyType(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_contact_reply\s+ADD COLUMN IF NOT EXISTS reply_type TEXT NOT NULL DEFAULT 'human'`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V60Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV60Migration_UpdateWorkspace_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_contact_reply`).WillReturnError(errors.New("boom"))

	err = (&V60Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reply_type")
}
