package http

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConnector : les connexions numerotees dans failing sont « mortes » (leur Ping echoue), les neuves sont saines.
type fakeConnector struct {
	mu      sync.Mutex
	opened  int
	deadIDs map[int]bool
	stuck   bool // le pool est coince : tout ping attend son delai, jusqu'a la fermeture forcee
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
func (c *fakeConn) Ping(ctx context.Context) error {
	c.c.mu.Lock()
	stuck := c.c.stuck
	c.c.mu.Unlock()
	if stuck {
		<-ctx.Done()
		return ctx.Err()
	}
	c.c.mu.Lock()
	defer c.c.mu.Unlock()
	if c.c.deadIDs[c.id] {
		return errors.New("context deadline exceeded")
	}
	return nil
}

// resetHealth isole l'etat global de la sante (sonde dediee, fermeture forcee, anti-rafale) pour un test.
func resetHealth(t *testing.T) (kills *int) {
	t.Helper()
	n := 0
	oldProbe, oldKill := healthProbe, healthKill
	healthProbe, healthKill = nil, func() int { n++; return 1 }
	killMu.Lock()
	lastKill = time.Time{}
	killMu.Unlock()
	t.Cleanup(func() {
		healthProbe, healthKill = oldProbe, oldKill
		killMu.Lock()
		lastKill = time.Time{}
		killMu.Unlock()
	})
	return &n
}

func newPool(t *testing.T, dead ...int) (*sql.DB, *fakeConnector) {
	t.Helper()
	resetHealth(t)
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

// Le cas du 09/11 : la base repond sur une connexion neuve, mais le pool est coince. Fermeture forcee, puis le
// pool repond : /healthz rend 200 sans redemarrage.
func TestPingWithRecovery_PoolCoinceBaseJoignable_FermeLesConnexionsDeForce(t *testing.T) {
	db, fc := newPool(t)
	kills := resetHealth(t)
	healthProbe = func(context.Context) error { return nil } // connexion dediee : la base accepte
	fc.stuck = true
	healthKill = func() int {
		*kills++
		fc.mu.Lock()
		fc.stuck = false // la fermeture forcee libere le pool
		fc.mu.Unlock()
		return 5
	}
	assert.NoError(t, pingWithRecovery(db))
	assert.Equal(t, 1, *kills)
}

// Base vraiment injoignable : 503, et on ne ferme rien (inutile et destructeur).
func TestPingWithRecovery_BaseInjoignable_NeFermeRien(t *testing.T) {
	db, fc := newPool(t)
	kills := resetHealth(t)
	healthProbe = func(context.Context) error { return errors.New("connection refused") }
	fc.stuck = true
	assert.Error(t, pingWithRecovery(db))
	assert.Equal(t, 0, *kills)
}

// Traefik sonde toutes les 2 s : la fermeture forcee n'est pas rejouee en rafale.
func TestKillStuckConns_AntiRafale(t *testing.T) {
	db, _ := newPool(t)
	kills := resetHealth(t)
	killStuckConns(db)
	killStuckConns(db)
	killStuckConns(db)
	assert.Equal(t, 1, *kills)
}

func TestSetHealthDSN_CableUneSondeDediee(t *testing.T) {
	resetHealth(t)
	SetHealthDSN("postgres://u:p@127.0.0.1:1/x?sslmode=disable")
	require.NotNil(t, healthProbe)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.Error(t, healthProbe(ctx), "personne n'ecoute : la sonde dediee echoue vraiment")

	assert.True(t, HealthProbeConfigured())

	resetHealth(t)
	SetHealthDSN("postgres://u:p@127.0.0.1:notaport/x") // DSN invalide : pas de sonde, la sante retombe sur le pool
	assert.Nil(t, healthProbe)
	assert.False(t, HealthProbeConfigured())
}

// resetState remet la sonde de fond a zero pour un test.
func resetState(t *testing.T) {
	t.Helper()
	dbHealth.mu.Lock()
	dbHealth.running, dbHealth.degraded, dbHealth.lastOK = false, false, time.Time{}
	dbHealth.mu.Unlock()
	t.Cleanup(func() {
		dbHealth.mu.Lock()
		dbHealth.running, dbHealth.degraded, dbHealth.lastOK = false, false, time.Time{}
		dbHealth.mu.Unlock()
	})
}

// Une bascule de moins de 45 s ne rend pas /healthz rouge ; au-dela, il l'est (le redemarrage reste le filet).
func TestDBHealthState_ToleranceDeBasculeEtRetourAuVert(t *testing.T) {
	resetState(t)
	known, _ := dbHealth.healthy()
	assert.False(t, known, "sonde de fond absente : l'appelant retombe sur un ping direct")

	dbHealth.mu.Lock()
	dbHealth.running, dbHealth.lastOK = true, time.Now()
	dbHealth.mu.Unlock()
	dbHealth.record(errors.New("base injoignable"))
	known, ok := dbHealth.healthy()
	assert.True(t, known)
	assert.True(t, ok, "20 s de panne : encore vert")

	dbHealth.mu.Lock()
	dbHealth.lastOK = time.Now().Add(-healthGrace - time.Second)
	dbHealth.mu.Unlock()
	_, ok = dbHealth.healthy()
	assert.False(t, ok, "panne longue : rouge")

	dbHealth.record(nil)
	_, ok = dbHealth.healthy()
	assert.True(t, ok, "base revenue : vert")
}

func TestStartDBHealth_LanceLaSondeUneSeuleFoisEtSArrete(t *testing.T) {
	resetState(t)
	ctx, cancel := context.WithCancel(context.Background())
	StartDBHealth(ctx, nil)
	StartDBHealth(ctx, nil) // idempotent
	known, ok := dbHealth.healthy()
	assert.True(t, known)
	assert.True(t, ok, "demarrage : vert pendant la fenetre de tolerance")
	cancel()
}
