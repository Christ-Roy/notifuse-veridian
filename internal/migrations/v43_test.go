package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

func TestV43Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 43.0, (&V43Migration{}).GetMajorVersion())
}

func TestV43Migration_HasSystemUpdate(t *testing.T) {
	assert.True(t, (&V43Migration{}).HasSystemUpdate())
}

func TestV43Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V43Migration{}).HasWorkspaceUpdate())
}

func TestV43Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V43Migration{}).ShouldRestartServer())
}

// expectV43Introspect mocke la requête information_schema pour une colonne donnée
// et renvoie le data_type configuré (`timestamp without time zone` ou
// `timestamp with time zone`).
func expectV43Introspect(mock sqlmock.Sqlmock, col, dataType string) {
	mock.ExpectQuery(`SELECT data_type\s+FROM information_schema.columns\s+WHERE table_name = 'veridian_plan' AND column_name = \$1`).
		WithArgs(col).
		WillReturnRows(sqlmock.NewRows([]string{"data_type"}).AddRow(dataType))
}

// expectV43Alter mocke l'ALTER COLUMN TYPE pour une colonne donnée.
func expectV43Alter(mock sqlmock.Sqlmock, col string) {
	mock.ExpectExec(
		`ALTER TABLE veridian_plan ALTER COLUMN ` + col + ` TYPE TIMESTAMP WITH TIME ZONE USING ` + col + ` AT TIME ZONE 'UTC'`,
	).WillReturnResult(sqlmock.NewResult(0, 0))
}

func TestV43Migration_UpdateSystem_Success_AllColumns(t *testing.T) {
	// Cas nominal : toutes les colonnes legacy sont en WITHOUT TIME ZONE,
	// la migration les retype toutes en WITH TIME ZONE.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	for _, col := range v43LegacyColumns {
		expectV43Introspect(mock, col, "timestamp without time zone")
		expectV43Alter(mock, col)
	}

	err = (&V43Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV43Migration_UpdateSystem_Idempotent_AllAlreadyMigrated(t *testing.T) {
	// Re-run sur DB où toutes les colonnes sont déjà en WITH TIME ZONE :
	// aucune commande ALTER n'est exécutée.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	for _, col := range v43LegacyColumns {
		expectV43Introspect(mock, col, "timestamp with time zone")
	}

	err = (&V43Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV43Migration_UpdateSystem_PartialMigration(t *testing.T) {
	// Cas intermédiaire (récupération après crash) : 2 colonnes déjà migrées,
	// 3 restantes. Seules les 3 restantes sont ALTER.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	alreadyMigrated := map[string]bool{
		"created_at": true,
		"updated_at": true,
	}

	for _, col := range v43LegacyColumns {
		if alreadyMigrated[col] {
			expectV43Introspect(mock, col, "timestamp with time zone")
			continue
		}
		expectV43Introspect(mock, col, "timestamp without time zone")
		expectV43Alter(mock, col)
	}

	err = (&V43Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV43Migration_UpdateSystem_IntrospectError(t *testing.T) {
	// Si introspection échoue, la migration remonte l'erreur (fail-stop, le
	// boot ne progresse pas avec un schéma partiellement modifié).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(`SELECT data_type`).WithArgs("created_at").
		WillReturnError(errors.New("connection lost"))

	err = (&V43Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "introspect veridian_plan.created_at")
}

func TestV43Migration_UpdateSystem_AlterError(t *testing.T) {
	// Si l'ALTER COLUMN plante (lock timeout, etc.), la migration remonte
	// l'erreur en wrappant le nom de la colonne.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV43Introspect(mock, "created_at", "timestamp without time zone")
	mock.ExpectExec(`ALTER TABLE veridian_plan ALTER COLUMN created_at`).
		WillReturnError(errors.New("lock timeout"))

	err = (&V43Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alter veridian_plan.created_at to TIMESTAMP WITH TIME ZONE")
}

func TestV43Migration_UpdateSystem_StopsOnFirstAlterError(t *testing.T) {
	// Si la 2e colonne plante en ALTER, on n'essaie pas les suivantes
	// (fail-stop sur les ALTER).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectV43Introspect(mock, "created_at", "timestamp without time zone")
	expectV43Alter(mock, "created_at")

	expectV43Introspect(mock, "updated_at", "timestamp without time zone")
	mock.ExpectExec(`ALTER TABLE veridian_plan ALTER COLUMN updated_at`).
		WillReturnError(errors.New("disk full"))

	err = (&V43Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alter veridian_plan.updated_at")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV43Migration_UpdateWorkspace_Noop(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V43Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
}

func TestV43Migration_LegacyColumns_Coverage(t *testing.T) {
	// Garde-fou : si quelqu'un retire/renomme une colonne de v43LegacyColumns,
	// ce test rappelle qu'il faut auditer le ticket
	// 2026-05-19-aligner-types-timestamp-veridian-plan.md.
	expected := []string{
		"created_at",
		"updated_at",
		"last_reset_at",
		"suspended_at",
		"deleted_at",
	}
	assert.Equal(t, expected, v43LegacyColumns,
		"v43LegacyColumns drift par rapport au ticket — relire 2026-05-19-aligner-types-timestamp-veridian-plan.md avant modif")
}

func TestV43Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 43.0 {
			return
		}
	}
	t.Fatal("V43Migration not registered")
}
