package pgdial

import (
	"net"
	"testing"

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
	assertKeepAlive(t, conn.(*net.TCPConn))

	conn2, err := New().Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	conn2.Close()
}
