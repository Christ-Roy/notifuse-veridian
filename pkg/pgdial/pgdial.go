// Package pgdial ouvre les connexions PostgreSQL avec un transport qui meurt vite quand le serveur disparait.
//
// Pourquoi : apres une bascule de la grappe (primaire redemarre, HAProxy d'entree), une connexion TCP morte
// sans RST laisse une requete en cours bloquee jusqu'a la retransmission du noyau (~15 min) : le 09/11/2026
// les taches de l'app attendaient 80 s par salves et la boite de reception chargeait en boucle. Un
// TCP_USER_TIMEOUT borne ce silence, et le keepalive detecte une connexion inactive coupee. Fiche 81.
package pgdial

import (
	"context"
	"database/sql"
	"net"
	"sync"
	"time"

	"github.com/lib/pq"
)

const (
	// UserTimeout : delai maximal sans accuse de reception de donnees emises avant que le noyau coupe la connexion.
	UserTimeout = 10 * time.Second
	// DialTimeout : delai de connexion quand le DSN n'en fixe pas.
	DialTimeout = 10 * time.Second

	keepAliveIdle     = 10 * time.Second
	keepAliveInterval = 3 * time.Second
	keepAliveCount    = 3
)

type dialer struct{ d net.Dialer }

// live : toutes les connexions ouvertes par ce dialer. Une connexion qui ne repond plus (session morte cote
// HAProxy apres une bascule : TCP reste « vivant », keepalive et TCP_USER_TIMEOUT ne la voient pas) occupe un
// emplacement du pool tant qu'elle n'est pas fermee ; CloseAll la ferme de force, la requete en vol echoue et
// le pool se reconnecte.
var live sync.Map // *trackedConn -> struct{}

type trackedConn struct {
	net.Conn
	once sync.Once
}

func track(c net.Conn, err error) (net.Conn, error) {
	if err != nil {
		return nil, err
	}
	t := &trackedConn{Conn: c}
	live.Store(t, struct{}{})
	return t, nil
}

func (t *trackedConn) Close() error {
	t.once.Do(func() { live.Delete(t) })
	return t.Conn.Close()
}

// CloseAll ferme de force toutes les connexions PostgreSQL ouvertes par l'application et rend leur nombre.
// Reserve au cas « la base repond sur une connexion neuve mais le pool ne rend plus la main ».
func CloseAll() int {
	n := 0
	live.Range(func(k, _ any) bool {
		_ = k.(*trackedConn).Close()
		n++
		return true
	})
	return n
}

func (x dialer) Dial(network, address string) (net.Conn, error) { return track(x.d.Dial(network, address)) }

// DialContext : la connexion respecte le delai du contexte (sans cela pq l'ignore et attend le delai du dialer).
func (x dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return track(x.d.DialContext(ctx, network, address))
}

func (x dialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	d := x.d
	d.Timeout = timeout
	return track(d.Dial(network, address))
}

// New : le dialer durci (keepalive 10 s / 3 s / 3 sondes, TCP_USER_TIMEOUT 10 s sous Linux).
func New() pq.Dialer {
	return dialer{d: net.Dialer{
		Timeout:         DialTimeout,
		KeepAliveConfig: net.KeepAliveConfig{Enable: true, Idle: keepAliveIdle, Interval: keepAliveInterval, Count: keepAliveCount},
		Control:         setUserTimeout,
	}}
}

// Open ouvre un pool avec le dialer durci. Un autre pilote que "postgres" (enveloppe de tracage) garde
// sql.Open tel quel : on ne casse pas le tracage.
func Open(driverName, dsn string) (*sql.DB, error) {
	if driverName != "postgres" {
		return sql.Open(driverName, dsn)
	}
	c, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	c.Dialer(New())
	return sql.OpenDB(c), nil
}
