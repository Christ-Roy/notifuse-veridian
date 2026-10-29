package veridian

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func twoSpecs(t *testing.T) *Registry {
	r := NewRegistry()
	require.NoError(t, r.Register(Spec{Number: 63, Lot: "L1", Name: "a", Workspace: []string{"CREATE TABLE IF NOT EXISTS a (id INT)"}}))
	require.NoError(t, r.Register(Spec{Number: 65, Lot: "L2", Name: "c", Workspace: []string{"CREATE TABLE IF NOT EXISTS c (id INT)"}, System: []string{"CREATE TABLE IF NOT EXISTS sc (id INT)"}}))
	return r
}

func expectApplied(mock sqlmock.Sqlmock, n int, applied bool) {
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs(n).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(applied))
}

// Le cas qui a motivé le registre : V65 déjà jouée, V63 livrée après, doit tout de même tourner.
func TestApplyWorkspace_PlaysMissingLowerNumberAfterHigherOne(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectApplied(mock, 63, false)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS a`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO veridian_migrations`).WithArgs(63, "L1", "a").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	expectApplied(mock, 65, true) // déjà jouée : rien d'exécuté
	mock.ExpectRollback()

	played, err := twoSpecs(t).ApplyWorkspace(context.Background(), db, "ws1")
	require.NoError(t, err)
	assert.Equal(t, []int{63}, played)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApplySystem_UsesSystemStatementsOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectApplied(mock, 63, false) // V63 sans instruction système : juste la marque
	mock.ExpectExec(`INSERT INTO veridian_migrations`).WithArgs(63, "L1", "a").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	expectApplied(mock, 65, false)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS sc`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO veridian_migrations`).WithArgs(65, "L2", "c").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	played, err := twoSpecs(t).ApplySystem(context.Background(), db)
	require.NoError(t, err)
	assert.Equal(t, []int{63, 65}, played)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyWorkspace_FailureRollsBackAndIsNotMarked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectApplied(mock, 63, false)
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS a`).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()

	played, err := twoSpecs(t).ApplyWorkspace(context.Background(), db, "ws1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "V63")
	assert.Empty(t, played)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyWorkspace_FuncReceivesWorkspaceID(t *testing.T) {
	r := NewRegistry()
	var got string
	require.NoError(t, r.Register(Spec{Number: 63, Lot: "L1", Name: "f", WorkspaceFunc: func(_ context.Context, id string, _ Executor) error { got = id; return nil }}))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectApplied(mock, 63, false)
	mock.ExpectExec(`INSERT INTO veridian_migrations`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	_, err = r.ApplyWorkspace(context.Background(), db, "ws-42")
	require.NoError(t, err)
	assert.Equal(t, "ws-42", got)
}

func TestApply_EmptyRegistryTouchesNothing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	played, err := NewRegistry().ApplyWorkspace(context.Background(), db, "ws")
	require.NoError(t, err)
	assert.Empty(t, played)
	assert.NoError(t, mock.ExpectationsWereMet()) // aucune requête attendue ni émise
}

type fakeSource struct {
	ids   []string
	conns map[string]*sql.DB
}

func (f fakeSource) IDs(context.Context) ([]string, error) { return f.ids, nil }
func (f fakeSource) Conn(_ context.Context, id string) (*sql.DB, error) {
	if c, ok := f.conns[id]; ok {
		return c, nil
	}
	return nil, errors.New("base introuvable")
}

type nopLog struct{ errs int }

func (l *nopLog) Info(string)  {}
func (l *nopLog) Error(string) { l.errs++ }

func TestRunAll_WorkspaceFailureDoesNotStopOthers(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(Spec{Number: 63, Lot: "L1", Name: "a", Workspace: []string{"CREATE TABLE IF NOT EXISTS a (id INT)"}}))

	sys, sysMock, _ := sqlmock.New()
	defer sys.Close()
	sysMock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectApplied(sysMock, 63, true)
	sysMock.ExpectRollback()

	good, goodMock, _ := sqlmock.New()
	defer good.Close()
	goodMock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnResult(sqlmock.NewResult(0, 0))
	expectApplied(goodMock, 63, false)
	goodMock.ExpectExec(`CREATE TABLE IF NOT EXISTS a`).WillReturnResult(sqlmock.NewResult(0, 0))
	goodMock.ExpectExec(`INSERT INTO veridian_migrations`).WillReturnResult(sqlmock.NewResult(1, 1))
	goodMock.ExpectCommit()

	log := &nopLog{}
	rep, err := r.RunAll(context.Background(), sys, fakeSource{ids: []string{"zz-orphan", "good"}, conns: map[string]*sql.DB{"good": good}}, log)
	require.NoError(t, err)
	assert.Equal(t, []int{63}, rep.Workspaces["good"])
	require.Len(t, rep.Failures, 1)
	assert.Equal(t, "zz-orphan", rep.Failures[0].Scope)
	assert.Equal(t, 1, log.errs, "l'échec doit être journalisé en erreur")
	assert.NoError(t, sysMock.ExpectationsWereMet())
	assert.NoError(t, goodMock.ExpectationsWereMet())
}

func TestRunAll_SystemFailureIsFatal(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(Spec{Number: 63, Lot: "L1", Name: "a", Workspace: []string{"CREATE TABLE IF NOT EXISTS a (id INT)"}}))
	sys, sysMock, _ := sqlmock.New()
	defer sys.Close()
	sysMock.ExpectExec(`CREATE TABLE IF NOT EXISTS veridian_migrations`).WillReturnError(errors.New("down"))
	_, err := r.RunAll(context.Background(), sys, fakeSource{}, &nopLog{})
	require.Error(t, err)
}
