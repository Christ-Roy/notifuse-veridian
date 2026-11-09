package pgdial

import (
	"context"
	"net"
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
