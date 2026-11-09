package http

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
	pkgDatabase "github.com/Notifuse/notifuse/pkg/database"
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

	healthLog logger.Logger // pose par StartDBHealth ; nil en test
)

// healthGrace : tant que la base a repondu il y a moins de ce delai, /healthz reste vert. Une bascule de la
// grappe dure 30 a 45 s : Traefik (1 s de delai) ne doit pas ecarter l'instance et Nomad ne doit pas la
// redemarrer pour cela ; le redemarrage reste le dernier filet (check_restart, 90 s) si la base ne revient pas.
const (
	healthGrace    = 45 * time.Second
	healthInterval = 2 * time.Second
)

type dbHealthState struct {
	mu       sync.Mutex
	running  bool
	lastOK   time.Time
	degraded bool
}

var dbHealth = &dbHealthState{}

// Healthy : la sonde de fond tourne ; la base a-t-elle repondu il y a moins de healthGrace ? Faux, faux si
// elle ne tourne pas (l'appelant retombe alors sur un ping direct).
func (s *dbHealthState) healthy() (known, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return false, false
	}
	return true, time.Since(s.lastOK) < healthGrace
}

// record enregistre un resultat de sonde et journalise les changements d'etat (pas chaque seconde).
func (s *dbHealthState) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		if s.degraded && healthLog != nil {
			healthLog.WithField("down_for", time.Since(s.lastOK).Round(time.Second).String()).Info("db health: base de nouveau joignable")
		}
		s.lastOK, s.degraded = time.Now(), false
		return
	}
	if !s.degraded && healthLog != nil {
		healthLog.WithField("error", err.Error()).Warn("db health: base injoignable")
	}
	s.degraded = true
}

// StartDBHealth lance la sonde de fond : toutes les 2 s elle juge la base (pool, sonde dediee, fermeture
// forcee) et /healthz ne fait plus que lire son verdict, sans attendre la base ni la charger a chaque requete.
func StartDBHealth(ctx context.Context, log logger.Logger) {
	dbHealth.mu.Lock()
	if dbHealth.running {
		dbHealth.mu.Unlock()
		return
	}
	dbHealth.running, dbHealth.lastOK = true, time.Now()
	healthLog = log
	dbHealth.mu.Unlock()
	go func() {
		t := time.NewTicker(healthInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				dbHealth.record(checkSystemDB())
			}
		}
	}()
}

func checkSystemDB() error {
	cm, err := pkgDatabase.GetConnectionManager()
	if err != nil {
		return err
	}
	db := cm.GetSystemConnection()
	if db == nil {
		return fmt.Errorf("connexion systeme absente")
	}
	return pingWithRecovery(db)
}

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
			return fmt.Errorf("sonde dediee (connexion neuve) en echec: %w", perr) // la base ne repond meme pas a une connexion neuve
		}
	}
	killed := killStuckConns(db)
	ctx, cancel = context.WithTimeout(context.Background(), healthPingRetryTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("pool toujours en echec apres fermeture forcee (%d connexions fermees, la sonde dediee repond): %w", killed, err)
	}
	return nil
}

// killStuckConns ferme de force les connexions, au plus une fois toutes les 10 s (Traefik sonde toutes les 2 s).
func killStuckConns(db *sql.DB) int {
	killMu.Lock()
	defer killMu.Unlock()
	if time.Since(lastKill) < healthMinKillInterval {
		return 0
	}
	lastKill = time.Now()
	n := healthKill()
	flushIdleConns(db)
	if healthLog != nil {
		healthLog.WithField("closed", n).Warn("db health: pool coince, connexions fermees de force")
	}
	return n
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
