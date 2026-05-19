package migrations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

func TestV34Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 34.0, (&V34Migration{}).GetMajorVersion())
}

func TestV34Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V34Migration{}).HasSystemUpdate())
}

func TestV34Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V34Migration{}).HasWorkspaceUpdate())
}

func TestV34Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V34Migration{}).ShouldRestartServer())
}

func TestV34Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS restored_at TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS purge_eligible_at TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS last_touched_at TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS lifecycle_reason TEXT`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V34Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV34Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB ou toutes les colonnes existent : tous IF NOT EXISTS no-op.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	for i := 0; i < 4; i++ {
		mock.ExpectExec(`ALTER TABLE veridian_plan`).WillReturnResult(sqlmock.NewResult(0, 0))
	}

	err = (&V34Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
}

func TestV34Migration_UpdateSystem_ErrorPropagation(t *testing.T) {
	// Si une des ALTER fail, on doit remonter l'erreur avec contexte.
	cases := []struct {
		name      string
		failIndex int
		expectMsg string
	}{
		{"restored_at fails", 0, "restored_at"},
		{"purge_eligible_at fails", 1, "purge_eligible_at"},
		{"last_touched_at fails", 2, "last_touched_at"},
		{"lifecycle_reason fails", 3, "lifecycle_reason"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			for i := 0; i < 4; i++ {
				if i == tc.failIndex {
					mock.ExpectExec(`ALTER TABLE veridian_plan`).WillReturnError(assert.AnError)
					break
				}
				mock.ExpectExec(`ALTER TABLE veridian_plan`).WillReturnResult(sqlmock.NewResult(0, 0))
			}

			err = (&V34Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectMsg)
		})
	}
}

func TestV34Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V34Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}
