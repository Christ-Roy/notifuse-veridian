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
	"database/sql/driver"
	"errors"
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
	return sql.OpenDB(&rwConnector{inner: c, attempts: rwAttempts, delay: rwDelay}), nil
}

const (
	rwAttempts = 6
	rwDelay    = 400 * time.Millisecond
)

// rwConnector refuse les connexions tombees sur un replica. Pendant une bascule (09/11/2026), HAProxy garde
// environ 2 s l'ancien leader devenu replica marque sain : une connexion ouverte a ce moment est en lecture
// seule, restait dans le pool et faisait echouer chaque ecriture (« cannot execute CREATE TABLE in a read-only
// transaction »). On verifie a l'ouverture (SHOW transaction_read_only) et on reessaie ~2 s, le temps que
// HAProxy ecarte l'ancien leader. L'application n'ecrit que sur le primaire : jamais de replica dans le pool.
type rwConnector struct {
	inner    driver.Connector
	attempts int
	delay    time.Duration
}

func (r *rwConnector) Driver() driver.Driver { return r.inner.Driver() }

func (r *rwConnector) Connect(ctx context.Context) (driver.Conn, error) {
	var lastErr error = driver.ErrBadConn
	for i := 0; i < r.attempts; i++ {
		conn, err := r.inner.Connect(ctx)
		if err != nil {
			return nil, err
		}
		ro, err := readOnly(ctx, conn)
		if err == nil && !ro {
			return conn, nil
		}
		_ = conn.Close()
		if err != nil {
			return nil, err
		}
		lastErr = errReadOnlyServer
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(r.delay):
		}
	}
	return nil, lastErr
}

var errReadOnlyServer = errors.New("pgdial: server is read-only (replica), no primary reachable yet")

// readOnly interroge le serveur ; une connexion qui ne sait pas repondre est consideree comme en erreur.
func readOnly(ctx context.Context, conn driver.Conn) (bool, error) {
	q, ok := conn.(driver.QueryerContext)
	if !ok {
		return false, nil // pilote sans requete directe : on ne peut pas verifier, on garde la connexion
	}
	rows, err := q.QueryContext(ctx, "SHOW transaction_read_only", nil)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil {
		return false, err
	}
	switch v := dest[0].(type) {
	case string:
		return v == "on", nil
	case []byte:
		return string(v) == "on", nil
	}
	return false, nil
}
