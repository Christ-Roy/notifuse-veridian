package migrations

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV38Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 38.0, (&V38Migration{}).GetMajorVersion())
}

func TestV38Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V38Migration{}).HasSystemUpdate())
}

func TestV38Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V38Migration{}).HasWorkspaceUpdate())
}

func TestV38Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V38Migration{}).ShouldRestartServer())
}

// expectV38AddColumns enchaine les 2 ALTER TABLE ADD COLUMN IF NOT EXISTS.
func expectV38AddColumns(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS emails_sent_lifetime BIGINT NOT NULL DEFAULT 0`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS activity_threshold_reached_at TIMESTAMP WITH TIME ZONE`).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestV38Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV38AddColumns(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET emails_sent_lifetime = emails_sent_this_month`).
		WillReturnResult(sqlmock.NewResult(0, 5))

	err = (&V38Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV38Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run sur DB où les colonnes existent déjà : IF NOT EXISTS no-op,
	// backfill ne touche que les rows avec lifetime = 0 AND this_month > 0.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV38AddColumns(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET emails_sent_lifetime = emails_sent_this_month`).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows updated = déjà backfillé

	err = (&V38Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV38Migration_UpdateSystem_AddLifetimeColumnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS emails_sent_lifetime`).
		WillReturnError(assert.AnError)

	err = (&V38Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add veridian_plan.emails_sent_lifetime")
}

func TestV38Migration_UpdateSystem_AddThresholdColumnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS emails_sent_lifetime`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE veridian_plan\s+ADD COLUMN IF NOT EXISTS activity_threshold_reached_at`).
		WillReturnError(assert.AnError)

	err = (&V38Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add veridian_plan.activity_threshold_reached_at")
}

func TestV38Migration_UpdateSystem_BackfillError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV38AddColumns(mock)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET emails_sent_lifetime = emails_sent_this_month`).
		WillReturnError(assert.AnError)

	err = (&V38Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill veridian_plan emails_sent_lifetime")
}

func TestV38Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V38Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV38Migration_RegisteredInRegistry(t *testing.T) {
	// Vérifie que la migration est bien enregistrée (le init() a été exécuté).
	found := false
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 38.0 {
			found = true
			break
		}
	}
	assert.True(t, found, "V38Migration doit être enregistrée dans le registry")
}
