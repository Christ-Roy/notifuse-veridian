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

func TestV62Migration_Metadata(t *testing.T) {
	m := &V62Migration{}
	assert.Equal(t, 62.0, m.GetMajorVersion())
	assert.False(t, m.HasSystemUpdate())
	assert.True(t, m.HasWorkspaceUpdate())
	assert.False(t, m.ShouldRestartServer())
}

func TestV62Migration_UpdateSystem_NoOp(t *testing.T) {
	assert.NoError(t, (&V62Migration{}).UpdateSystem(context.Background(), &config.Config{}, nil))
}

func TestV62Migration_UpdateWorkspace_AddsColumnsTableAndIndexes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE email_queue[\s\S]*defer_reason VARCHAR\(24\)[\s\S]*decision_logged_at TIMESTAMPTZ`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_send_decisions`).WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 0; i < 4; i++ {
		mock.ExpectExec(`CREATE INDEX IF NOT EXISTS idx_veridian_send_decisions_`).WillReturnResult(sqlmock.NewResult(0, 0))
	}

	require.NoError(t, (&V62Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV62Migration_UpdateWorkspace_IsExpandOnly(t *testing.T) {
	// Expand & Contract : rien de destructif, tout est idempotent.
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		for _, banned := range []string{"DROP ", "TRUNCATE", "RENAME ", "CONCURRENTLY"} {
			if containsFold(actual, banned) {
				return errors.New("statement non expand-only : " + banned)
			}
		}
		return nil
	})))
	require.NoError(t, err)
	defer db.Close()
	for i := 0; i < 6; i++ {
		mock.ExpectExec("ignored").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	require.NoError(t, (&V62Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func containsFold(s, sub string) bool {
	return len(sub) > 0 && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'a' && a <= 'z' {
				a -= 32
			}
			if b >= 'a' && b <= 'z' {
				b -= 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func TestV62Migration_UpdateWorkspace_ErrorOnColumns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`ALTER TABLE email_queue`).WillReturnError(errors.New("boom"))
	err = (&V62Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deferral columns")
}

func TestV62Migration_UpdateWorkspace_ErrorOnTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`ALTER TABLE email_queue`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`CREATE TABLE`).WillReturnError(errors.New("boom"))
	err = (&V62Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "veridian_send_decisions")
}
