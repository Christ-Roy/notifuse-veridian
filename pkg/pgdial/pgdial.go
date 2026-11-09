// Package pgdial ouvre les connexions PostgreSQL avec un transport qui meurt vite quand le serveur disparait.
//
// Pourquoi : apres une bascule de la grappe (primaire redemarre, HAProxy d'entree), une connexion TCP morte
// sans RST laisse une requete en cours bloquee jusqu'a la retransmission du noyau (~15 min) : le 09/11/2026
// les taches de l'app attendaient 80 s par salves et la boite de reception chargeait en boucle. Un
// TCP_USER_TIMEOUT borne ce silence, et le keepalive detecte une connexion inactive coupee. Fiche 81.
package pgdial

import (
	"database/sql"
	"net"
	"time"

	"github.com/lib/pq"
)

const (
	// UserTimeout : delai maximal sans accuse de reception de donnees emises avant que le noyau coupe la connexion.
	UserTimeout = 20 * time.Second
	// DialTimeout : delai de connexion quand le DSN n'en fixe pas.
	DialTimeout = 10 * time.Second

	keepAliveIdle     = 15 * time.Second
	keepAliveInterval = 5 * time.Second
	keepAliveCount    = 3
)

type dialer struct{ d net.Dialer }

func (x dialer) Dial(network, address string) (net.Conn, error) { return x.d.Dial(network, address) }

func (x dialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	d := x.d
	d.Timeout = timeout
	return d.Dial(network, address)
}

// New : le dialer durci (keepalive 15 s / 5 s / 3 sondes, TCP_USER_TIMEOUT sous Linux).
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
