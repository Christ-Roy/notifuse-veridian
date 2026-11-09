package http

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/pkg/pgdial"
)

const (
	healthPingFirstTimeout = 1500 * time.Millisecond // sur le pool, qui peut etre sature de connexions bloquees
	healthProbeTimeout     = 1500 * time.Millisecond // sur une connexion DEDIEE, hors pool
	healthPingRetryTimeout = 1500 * time.Millisecond // apres fermeture forcee des connexions ; total < 5 s (check Nomad)
	healthMinKillInterval  = 10 * time.Second
)

var (
	// healthProbe : ping sur une connexion dediee, hors du pool applicatif (cable par SetHealthDSN).
	healthProbe func(ctx context.Context) error
	// healthKill : ferme de force les connexions de l'application (pgdial.CloseAll).
	healthKill = pgdial.CloseAll

	killMu   sync.Mutex
	lastKill time.Time
)

// SetHealthDSN fournit le DSN de la base systeme pour la sonde dediee. Appele au cablage (veridian_wiring.go).
func SetHealthDSN(dsn string) {
	db, err := pgdial.Open("postgres", dsn)
	if err != nil {
		return
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0) // chaque sonde se connecte pour de bon : elle prouve que la base accepte des connexions
	healthProbe = func(ctx context.Context) error { return db.PingContext(ctx) }
}

// HealthProbeConfigured : la sonde dediee est-elle cablee ?
func HealthProbeConfigured() bool { return healthProbe != nil }

// pingWithRecovery juge la base SANS condamner l'application pour un pool abime.
//
// 09/11/2026, deux bascules de la grappe sous sonde : le nouveau primaire repondait des 19:01:25 mais /healthz
// echouait jusqu'au redemarrage Nomad (2 min). Le pool applicatif etait plein de connexions bloquees (sessions
// mortes cote HAProxy : le TCP reste vivant, donc ni keepalive ni TCP_USER_TIMEOUT ne les voient) et le ping
// attendait un emplacement qui ne se liberait jamais. Un redemarrage ne devrait pas etre le premier remede :
//  1. ping sur le pool ; 2. si echec, ping sur une connexion DEDIEE : si elle echoue, la base est vraiment
//  injoignable (503) ; 3. sinon la base repond et c'est le pool qui est coince : on ferme de force les connexions
//  (les requetes bloquees echouent, le pool se reconnecte) puis on reping le pool.
func pingWithRecovery(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthPingFirstTimeout)
	err := db.PingContext(ctx)
	cancel()
	if err == nil {
		return nil
	}
	if healthProbe != nil {
		pctx, pcancel := context.WithTimeout(context.Background(), healthProbeTimeout)
		perr := healthProbe(pctx)
		pcancel()
		if perr != nil {
			return perr // la base ne repond meme pas a une connexion neuve
		}
	}
	killStuckConns(db)
	ctx, cancel = context.WithTimeout(context.Background(), healthPingRetryTimeout)
	defer cancel()
	return db.PingContext(ctx)
}

// killStuckConns ferme de force les connexions, au plus une fois toutes les 10 s (Traefik sonde toutes les 2 s).
func killStuckConns(db *sql.DB) {
	killMu.Lock()
	defer killMu.Unlock()
	if time.Since(lastKill) < healthMinKillInterval {
		return
	}
	lastKill = time.Now()
	healthKill()
	flushIdleConns(db)
}

// flushIdleConns ferme les connexions inactives (database/sql ne fait pas autrement) puis rend la capacite.
func flushIdleConns(db *sql.DB) {
	keep := db.Stats().MaxOpenConnections / 2
	if keep < 1 {
		keep = 1
	}
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(keep)
}
