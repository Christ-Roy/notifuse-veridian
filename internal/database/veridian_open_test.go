package database

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenPostgres_RendUnPoolEtGardeLesAutresPilotes(t *testing.T) {
	db, err := OpenPostgres("postgres", "postgres://u:p@127.0.0.1:1/x?sslmode=disable")
	require.NoError(t, err)
	defer db.Close()
	assert.Error(t, db.Ping(), "aucun serveur : la connexion echoue, l'ouverture non")

	_, err = OpenPostgres("pilote-inconnu", "x")
	assert.Error(t, err)
}

func TestTransientDBStartupError(t *testing.T) {
	for _, msg := range []string{
		"failed to create table: pq: cannot execute CREATE TABLE in a read-only transaction",
		"pq: the database system is shutting down", "driver: bad connection", "EOF", "dial tcp: connection refused",
	} {
		assert.True(t, transientDBStartupError(errors.New(msg)), msg)
	}
	assert.False(t, transientDBStartupError(errors.New("pq: permission denied for table users")))
	assert.False(t, transientDBStartupError(errors.New("syntax error at or near")))
}

// Le demarrage pendant une bascule reessaie au lieu de sortir en erreur ; une erreur durable remonte aussitot.
func TestInitializeDatabaseResilient_ReessaieTantQueLaBaseBascule(t *testing.T) {
	oldFn, oldDelay, oldWindow := initDatabase, initRetryDelay, initRetryWindow
	defer func() { initDatabase, initRetryDelay, initRetryWindow = oldFn, oldDelay, oldWindow }()
	initRetryDelay, initRetryWindow = time.Millisecond, time.Second

	calls := 0
	initDatabase = func(*sql.DB, string) error {
		calls++
		if calls < 3 {
			return errors.New("pq: cannot execute CREATE TABLE in a read-only transaction")
		}
		return nil
	}
	assert.NoError(t, InitializeDatabaseResilient(nil, "root@x.test"))
	assert.Equal(t, 3, calls)

	calls = 0
	initDatabase = func(*sql.DB, string) error { calls++; return errors.New("permission denied for table users") }
	assert.Error(t, InitializeDatabaseResilient(nil, "root@x.test"))
	assert.Equal(t, 1, calls, "erreur durable : aucune nouvelle tentative")

	initRetryWindow = 20 * time.Millisecond
	calls = 0
	initDatabase = func(*sql.DB, string) error { calls++; return errors.New("EOF") }
	assert.Error(t, InitializeDatabaseResilient(nil, "root@x.test"), "base qui ne revient pas : on abandonne apres la fenetre")
	assert.Greater(t, calls, 1)
}
