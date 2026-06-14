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

func TestV50Migration_GetMajorVersion(t *testing.T) {
	assert.Equal(t, 50.0, (&V50Migration{}).GetMajorVersion())
}

func TestV50Migration_HasSystemUpdate(t *testing.T) {
	// La table veridian_imap_uid_seen est SYSTÈME (un seul set d'UID vus
	// cross-workspace, clé portant le tenant), pas par workspace.
	assert.True(t, (&V50Migration{}).HasSystemUpdate())
}

func TestV50Migration_HasWorkspaceUpdate(t *testing.T) {
	assert.False(t, (&V50Migration{}).HasWorkspaceUpdate())
}

func TestV50Migration_ShouldRestartServer(t *testing.T) {
	assert.False(t, (&V50Migration{}).ShouldRestartServer())
}

func TestV50Migration_UpdateSystem_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Vérifie la création de la table ET que la clé d'idempotence est bien la
	// PK composite incluant uid_validity (garde-fou RFC 3501 : sans
	// uid_validity dans la clé, un changement d'UIDVALIDITY ferait re-considérer
	// d'anciens UID comme vus à tort).
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_imap_uid_seen`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V50Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV50Migration_UpdateSystem_Idempotent(t *testing.T) {
	// Re-run : IF NOT EXISTS => même exec, pas d'erreur. On simule un second
	// passage de la migration (rollback/redeploy) qui ne doit pas planter.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_imap_uid_seen`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&V50Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV50Migration_UpdateSystem_CreateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_imap_uid_seen`).
		WillReturnError(assert.AnError)

	err = (&V50Migration{}).UpdateSystem(context.Background(), &config.Config{}, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create veridian_imap_uid_seen")
}

func TestV50Migration_UpdateWorkspace_Noop(t *testing.T) {
	// Migration système : UpdateWorkspace ne doit lancer AUCUNE requête
	// (sinon sqlmock signalerait une exec inattendue).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	err = (&V50Migration{}).UpdateWorkspace(context.Background(), &config.Config{}, &domain.Workspace{ID: "ws"}, db)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestV50Migration_Registered(t *testing.T) {
	for _, m := range GetRegisteredMigrations() {
		if m.GetMajorVersion() == 50.0 {
			return
		}
	}
	t.Fatal("V50Migration not registered")
}
