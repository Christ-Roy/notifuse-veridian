package pgdial

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen_PostgresRendUnPoolPreteSansSeConnecter(t *testing.T) {
	db, err := Open("postgres", "postgres://u:p@127.0.0.1:1/x?sslmode=disable")
	require.NoError(t, err)
	defer db.Close()
	assert.Error(t, db.Ping(), "personne n'ecoute : l'erreur vient de la connexion, pas de l'ouverture")
}

func TestOpen_DsnInvalideEtAutrePiloteGardentSqlOpen(t *testing.T) {
	_, err := Open("postgres", "postgres://u:p@127.0.0.1:notaport/x")
	assert.Error(t, err)
	_, err = Open("pilote-inconnu", "x")
	assert.Error(t, err, "un autre pilote passe par sql.Open (le tracage reste intact)")
}

func TestNew_DialerAvecKeepaliveEtDelaiDeConnexion(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
		}
	}()

	conn, err := New().DialTimeout("tcp", ln.Addr().String(), DialTimeout)
	require.NoError(t, err)
	defer conn.Close()
	assertKeepAlive(t, conn.(*trackedConn).Conn.(*net.TCPConn))

	conn2, err := New().Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	conn2.Close()
}

func TestDialer_DialContextRespecteLeContexte(t *testing.T) {
	d := New().(interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.DialContext(ctx, "tcp", "127.0.0.1:1")
	assert.Error(t, err, "contexte deja annule : pas de connexion")
}

// Une connexion « bloquee » (le serveur ne repond plus) est fermee de force : la lecture en cours echoue
// au lieu d'attendre 30 min, et le pool peut se reconnecter.
func TestCloseAll_DebloqueUneLectureEnCours(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(5 * time.Second) // serveur muet
		}
	}()
	conn, err := New().Dial("tcp", ln.Addr().String())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); done <- err }()
	time.Sleep(100 * time.Millisecond)
	assert.GreaterOrEqual(t, CloseAll(), 1)
	select {
	case err := <-done:
		assert.Error(t, err, "la lecture bloquee est interrompue")
	case <-time.After(2 * time.Second):
		t.Fatal("la lecture est restee bloquee apres CloseAll")
	}
	assert.Equal(t, 0, CloseAll(), "plus rien a fermer, la connexion s'est retiree du registre")
}

// fakeServer : n premieres connexions sur un replica (read-only), puis le primaire.
type fakeServer struct {
	mu       sync.Mutex
	opened   int
	replicas int
	closed   int
}

func (f *fakeServer) Connect(context.Context) (driver.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	return &fakeSrvConn{s: f, ro: f.opened <= f.replicas}, nil
}
func (f *fakeServer) Driver() driver.Driver { return nil }

type fakeSrvConn struct {
	s  *fakeServer
	ro bool
}

func (c *fakeSrvConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("non utilise") }
func (c *fakeSrvConn) Close() error                         { c.s.mu.Lock(); c.s.closed++; c.s.mu.Unlock(); return nil }
func (c *fakeSrvConn) Begin() (driver.Tx, error)            { return nil, errors.New("non utilise") }
func (c *fakeSrvConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	v := "off"
	if c.ro {
		v = "on"
	}
	return &oneRow{v: v}, nil
}

type oneRow struct {
	v    string
	done bool
}

func (r *oneRow) Columns() []string { return []string{"transaction_read_only"} }
func (r *oneRow) Close() error      { return nil }
func (r *oneRow) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.v
	return nil
}

// Une connexion tombee sur un replica est jetee et on reessaie : le pool ne garde jamais de replica.
func TestRwConnector_RefuseUnReplicaEtReessaie(t *testing.T) {
	srv := &fakeServer{replicas: 2}
	rc := &rwConnector{inner: srv, attempts: 5, delay: time.Millisecond}
	conn, err := rc.Connect(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, conn)
	assert.Equal(t, 3, srv.opened, "deux replicas refuses, le troisieme est le primaire")
	assert.Equal(t, 2, srv.closed, "les replicas ont ete fermes")
}

func TestRwConnector_AbandonneSiAucunPrimaire(t *testing.T) {
	srv := &fakeServer{replicas: 100}
	rc := &rwConnector{inner: srv, attempts: 3, delay: time.Millisecond}
	_, err := rc.Connect(context.Background())
	assert.ErrorIs(t, err, errReadOnlyServer)
	assert.Equal(t, 3, srv.opened)
	assert.Equal(t, 3, srv.closed)
}

func TestRwConnector_ContexteAnnuleInterrompt(t *testing.T) {
	srv := &fakeServer{replicas: 100}
	rc := &rwConnector{inner: srv, attempts: 50, delay: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := rc.Connect(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
