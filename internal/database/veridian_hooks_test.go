package database

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// La base d'un workspace neuf est fermee a PUBLIC, rouverte au proprietaire et au role de surveillance.
func TestRestrictWorkspaceDatabase_FermeAPublicEtRouvreAuProprietaireEtALaSurveillance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`REVOKE ALL ON DATABASE "notifuse_ws_acme" FROM PUBLIC`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`GRANT CONNECT ON DATABASE "notifuse_ws_acme" TO "notifuse"`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM pg_roles WHERE rolname = \$1\)`).WithArgs("pgsaas_mon").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(`GRANT CONNECT ON DATABASE "notifuse_ws_acme" TO "pgsaas_mon"`).WillReturnResult(sqlmock.NewResult(0, 0))

	require.NoError(t, restrictWorkspaceDatabase(db, "notifuse_ws_acme", "notifuse"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// Dev et staging n'ont pas le role de surveillance : on ne le sert pas, sans erreur.
func TestRestrictWorkspaceDatabase_SansRoleDeSurveillance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(`REVOKE ALL ON DATABASE`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`GRANT CONNECT ON DATABASE .* TO "app"`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`pg_roles`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	require.NoError(t, restrictWorkspaceDatabase(db, "app_ws_x", "app"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// Un echec du REVOKE fait echouer la creation : jamais de base ouverte a PUBLIC en silence.
func TestRestrictWorkspaceDatabase_EchecDuRevokeRemonte(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`REVOKE ALL ON DATABASE`).WillReturnError(errors.New("permission denied"))

	err = restrictWorkspaceDatabase(db, "notifuse_ws_x", "notifuse")
	assert.ErrorContains(t, err, "revoke public access")
}

func TestRestrictWorkspaceDatabase_EchecDuGrantProprietaireRemonte(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`REVOKE ALL`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`GRANT CONNECT`).WillReturnError(errors.New("boom"))
	assert.ErrorContains(t, restrictWorkspaceDatabase(db, "notifuse_ws_x", "notifuse"), "grant owner access")
}
