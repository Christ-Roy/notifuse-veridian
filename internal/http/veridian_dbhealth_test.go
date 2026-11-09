package http

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConnector : les connexions numerotees dans failing sont « mortes » (leur Ping echoue), les neuves sont saines.
type fakeConnector struct {
	mu      sync.Mutex
	opened  int
	deadIDs map[int]bool
}

type fakeConn struct {
	id int
	c  *fakeConnector
}

func (f *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	return &fakeConn{id: f.opened, c: f}, nil
}
func (f *fakeConnector) Driver() driver.Driver { return nil }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("non utilise") }
func (c *fakeConn) Close() error                         { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)            { return nil, errors.New("non utilise") }
func (c *fakeConn) Ping(context.Context) error {
	c.c.mu.Lock()
	defer c.c.mu.Unlock()
	if c.c.deadIDs[c.id] {
		return errors.New("context deadline exceeded")
	}
	return nil
}

func newPool(t *testing.T, dead ...int) (*sql.DB, *fakeConnector) {
	t.Helper()
	fc := &fakeConnector{deadIDs: map[int]bool{}}
	for _, d := range dead {
		fc.deadIDs[d] = true
	}
	db := sql.OpenDB(fc)
	db.SetMaxOpenConns(10)
	t.Cleanup(func() { _ = db.Close() })
	return db, fc
}

// Une connexion morte au premier ping ne fait pas echouer la sante : on reessaie sur une connexion neuve.
func TestPingWithRecovery_UneConnexionMorteNeCondamnePas(t *testing.T) {
	db, fc := newPool(t, 1)
	assert.NoError(t, pingWithRecovery(db))
	assert.Equal(t, 2, fc.opened, "la connexion morte a ete jetee, une neuve a repondu")
}

func TestPingWithRecovery_BaseVraimentInjoignableEchoue(t *testing.T) {
	db, _ := newPool(t, 1, 2, 3)
	assert.Error(t, pingWithRecovery(db))
}

func TestPingWithRecovery_PoolSainSansReconnexion(t *testing.T) {
	db, fc := newPool(t)
	require.NoError(t, pingWithRecovery(db))
	require.NoError(t, pingWithRecovery(db))
	assert.Equal(t, 1, fc.opened)
}

func TestFlushIdleConns_RendLaCapaciteDuPool(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(10)
	flushIdleConns(db)
	flushIdleConns(db) // idempotent ; le pool reste utilisable
	assert.Equal(t, 10, db.Stats().MaxOpenConnections)
}
