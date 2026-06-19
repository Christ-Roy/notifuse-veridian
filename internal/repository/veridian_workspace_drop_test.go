package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
)

func TestVeridianWorkspaceDBName(t *testing.T) {
	assert.Equal(t, "notifuse_ws_abc123", VeridianWorkspaceDBName("notifuse", "abc123"))
	// Les tirets deviennent des underscores (convention upstream DeleteDatabase).
	assert.Equal(t, "notifuse_ws_a_b_c", VeridianWorkspaceDBName("notifuse", "a-b-c"))
	assert.Equal(t, "nf_ws_x", VeridianWorkspaceDBName("nf", "x"))
}

func TestVeridianWorkspaceIDFromDBName(t *testing.T) {
	assert.Equal(t, "abc123", VeridianWorkspaceIDFromDBName("notifuse", "notifuse_ws_abc123"))
	assert.Equal(t, "canary_pro", VeridianWorkspaceIDFromDBName("notifuse", "notifuse_ws_canary_pro"))
	// Nom qui ne matche pas le prefix → renvoyé tel quel.
	assert.Equal(t, "other_db", VeridianWorkspaceIDFromDBName("notifuse", "other_db"))
}

func TestWorkspaceRepository_VeridianForceDropDatabase_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &workspaceRepository{systemDB: db, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}

	mock.ExpectExec(regexp.QuoteMeta("REVOKE CONNECT ON DATABASE notifuse_ws_xyz FROM PUBLIC")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("pg_terminate_backend").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DROP DATABASE IF EXISTS notifuse_ws_xyz WITH (FORCE)")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.VeridianForceDropDatabase(context.Background(), "xyz", nil)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianForceDropByDBName_DropFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("REVOKE CONNECT").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("pg_terminate_backend").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP DATABASE").WillReturnError(errors.New("permission denied"))

	err = veridianForceDropByDBName(context.Background(), db, "notifuse_ws_xyz", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestVeridianForceDropByDBName_RevokeAndTerminateBestEffort(t *testing.T) {
	// Une erreur sur revoke et terminate ne doit PAS empêcher le DROP final.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("REVOKE CONNECT").WillReturnError(errors.New("db absent"))
	mock.ExpectExec("pg_terminate_backend").WillReturnError(errors.New("db absent"))
	mock.ExpectExec("DROP DATABASE").WillReturnResult(sqlmock.NewResult(0, 0))

	err = veridianForceDropByDBName(context.Background(), db, "notifuse_ws_xyz", nil)
	require.NoError(t, err, "revoke/terminate errors must be swallowed; only DROP failure matters")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestVeridianForceDropByDBName_NilSystemDB(t *testing.T) {
	err := veridianForceDropByDBName(context.Background(), nil, "notifuse_ws_xyz", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil systemDB")
}

func TestVeridianForceDropByDBName_UnsafeIdentifierRefused(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Aucune requête ne doit partir : on refuse AVANT toute exec.
	err = veridianForceDropByDBName(context.Background(), db, "x; DROP TABLE users", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe db identifier")
	assert.NoError(t, mock.ExpectationsWereMet(), "no SQL must be issued for an unsafe identifier")
}

func TestWorkspaceRepository_VeridianListOrphanWorkspaceDBs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &workspaceRepository{systemDB: db, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT datname FROM pg_database WHERE datname LIKE`)).
		WithArgs("notifuse_ws_%").
		WillReturnRows(sqlmock.NewRows([]string{"datname"}).
			AddRow("notifuse_ws_keep1").
			AddRow("notifuse_ws_orphan1").
			AddRow("notifuse_ws_orphan2"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM workspaces`)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("keep1"))

	orphans, err := repo.VeridianListOrphanWorkspaceDBs(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"notifuse_ws_orphan1", "notifuse_ws_orphan2"}, orphans)
	assert.NotContains(t, orphans, "notifuse_ws_keep1")
}

func TestVeridianListOrphanWorkspaceDBs_RecordWithHyphen(t *testing.T) {
	// Un record id avec tiret doit matcher la base avec underscore.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`pg_database`).WithArgs("notifuse_ws_%").
		WillReturnRows(sqlmock.NewRows([]string{"datname"}).
			AddRow("notifuse_ws_a_b_c").
			AddRow("notifuse_ws_zzz"))
	mock.ExpectQuery(`FROM workspaces`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a-b-c"))

	orphans, err := veridianListOrphanWorkspaceDBs(context.Background(), db, "notifuse")
	require.NoError(t, err)
	assert.Equal(t, []string{"notifuse_ws_zzz"}, orphans, "a-b-c record must match notifuse_ws_a_b_c")
}

func TestVeridianListOrphanWorkspaceDBs_NilDB(t *testing.T) {
	_, err := veridianListOrphanWorkspaceDBs(context.Background(), nil, "notifuse")
	require.Error(t, err)
}

func TestWorkspaceRepository_VeridianWorkspaceDBPrefix(t *testing.T) {
	repo := &workspaceRepository{dbConfig: &config.DatabaseConfig{Prefix: "nf"}}
	assert.Equal(t, "nf", repo.VeridianWorkspaceDBPrefix())
}

func TestWorkspaceRepository_VeridianDeleteWorkspaceSystemRecord_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &workspaceRepository{systemDB: db, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}

	// Ordre attendu : workspaces D'ABORD (coupe la ré-élection worker), puis annexes.
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workspaces WHERE id = $1`)).
		WithArgs("tst123").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM user_workspaces WHERE workspace_id = $1`)).
		WithArgs("tst123").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workspace_invitations WHERE workspace_id = $1`)).
		WithArgs("tst123").WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.VeridianDeleteWorkspaceSystemRecord(context.Background(), "tst123")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepository_VeridianDeleteWorkspaceSystemRecord_Idempotent(t *testing.T) {
	// 0 row supprimée (record déjà absent) n'est PAS une erreur.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &workspaceRepository{systemDB: db, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workspaces WHERE id = $1`)).
		WithArgs("gone").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM user_workspaces WHERE workspace_id = $1`)).
		WithArgs("gone").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workspace_invitations WHERE workspace_id = $1`)).
		WithArgs("gone").WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.VeridianDeleteWorkspaceSystemRecord(context.Background(), "gone")
	require.NoError(t, err, "record already absent (0 rows) must be a no-op success")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepository_VeridianDeleteWorkspaceSystemRecord_WorkspacesError(t *testing.T) {
	// Une erreur sur le DELETE FROM workspaces (le 1er, le plus important) est remontée
	// et stoppe la suite (on ne touche pas aux annexes si le record principal a échoué).
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &workspaceRepository{systemDB: db, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM workspaces WHERE id = $1`)).
		WithArgs("tst123").WillReturnError(errors.New("boom"))

	err = repo.VeridianDeleteWorkspaceSystemRecord(context.Background(), "tst123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete workspace record")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkspaceRepository_VeridianDeleteWorkspaceSystemRecord_NilDB(t *testing.T) {
	repo := &workspaceRepository{systemDB: nil, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}
	err := repo.VeridianDeleteWorkspaceSystemRecord(context.Background(), "tst123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil systemDB")
}

func TestWorkspaceRepository_VeridianWorkspaceDBExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &workspaceRepository{systemDB: db, dbConfig: &config.DatabaseConfig{Prefix: "notifuse"}}

	// Présente.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`)).
		WithArgs("notifuse_ws_alive").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	exists, err := repo.VeridianWorkspaceDBExists(context.Background(), "alive")
	require.NoError(t, err)
	assert.True(t, exists)

	// Absente.
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`)).
		WithArgs("notifuse_ws_gone").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	exists, err = repo.VeridianWorkspaceDBExists(context.Background(), "gone")
	require.NoError(t, err)
	assert.False(t, exists)
}
