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

func TestV31Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 31.0, (&V31Migration{}).GetMajorVersion())
}

func TestV31Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V31Migration{}).HasSystemUpdate())
}

func TestV31Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V31Migration{}).HasWorkspaceUpdate())
}

func TestV31Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V31Migration{}).ShouldRestartServer())
}

func TestV31Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Backfill veridian_plan pour les workspaces sans row.
	// On vérifie la forme de la query : INSERT ... SELECT ... LEFT JOIN ... WHERE IS NULL.
	mock.ExpectExec(`INSERT INTO veridian_plan\s+\(\s*workspace_id,\s*plan,\s*status`).
		WillReturnResult(sqlmock.NewResult(0, 9))

	err = (&V31Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV31Migration_UpdateSystem_Idempotent(t *testing.T) {
	// 2e exécution : LEFT JOIN ... WHERE IS NULL renvoie 0 row → 0 INSERT.
	// On simule en répondant 0 rows affected — le code ne doit pas considérer
	// ça comme une erreur (migration safe à re-jouer).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`INSERT INTO veridian_plan`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V31Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err, "migration must be idempotent — 0 rows affected is OK")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV31Migration_UpdateSystem_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`INSERT INTO veridian_plan`).WillReturnError(assert.AnError)

	err = (&V31Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill veridian_plan")
}

func TestV31Migration_UpdateWorkspace_Noop(t *testing.T) {
	// HasWorkspaceUpdate=false donc UpdateWorkspace ne devrait jamais être appelée,
	// mais on garde un test garde-fou pour s'assurer qu'elle ne touche pas au DB.
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V31Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}
