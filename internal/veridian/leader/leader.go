// Package leader : un seul exemplaire de Mail mène ce qui ne supporte pas d'être fait deux fois (27/11/2026, VER-270).
//
// Pour tenir deux exemplaires en prod (perte d'un nœud sans coupure), le HTTP est servi par les deux, mais le travail de fond
// reste unique : l'envoi (ses portes de débit, l'étalement et les disjoncteurs vivent en mémoire du processus : deux workers
// enverraient deux fois plus vite par classe), la relève IMAP des boîtes des clients, la livraison des webhooks (prises sans
// verrou par l'amont : deux livraisons) et le renouvellement du certificat du relais.
//
// Le leader tient un verrou consultatif PostgreSQL de session sur une connexion dédiée de la base système. Il le perd quand sa
// connexion meurt (instance arrêtée ou tuée : PostgreSQL libère le verrou avec la session). Chaque tour, il vérifie dans
// pg_locks qu'il le tient encore ; au moindre doute, il cesse de mener. Garantie : au plus un leader ; pendant une bascule,
// aucun pendant quelques secondes (l'envoi attend, rien ne part en double).
package leader

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// Key : la clé du verrou (classid 0, objid Key dans pg_locks). Un verrou consultatif vit dans UNE base : le staging et la prod,
// bases système distinctes, ont chacun leur leader.
const Key int64 = 270_270

// Every : l'intervalle entre deux vérifications. Une bascule prend au plus Every après la libération du verrou.
const Every = 5 * time.Second

// Gate dit si cet exemplaire mène.
type Gate interface{ Leads() bool }

// Always : mène toujours. Un seul exemplaire (poste, tests) ou VERIDIAN_LEADER=off (coupe-circuit : retour au comportement
// d'avant, à n'employer qu'avec count = 1).
type Always struct{}

func (Always) Leads() bool { return true }

// Elector : l'élection par verrou consultatif.
type Elector struct {
	DB    *sql.DB
	Log   logger.Logger
	Every time.Duration

	held atomic.Bool
	mu   sync.Mutex
	conn *sql.Conn
}

func (e *Elector) Leads() bool { return e.held.Load() }

// New : l'élection, ou Always quand VERIDIAN_LEADER=off ou sans base.
func New(db *sql.DB, log logger.Logger) Gate {
	if db == nil || os.Getenv("VERIDIAN_LEADER") == "off" {
		return Always{}
	}
	return &Elector{DB: db, Log: log, Every: Every}
}

// Run tient l'élection jusqu'à l'annulation de ctx, puis rend le verrou (l'exemplaire suivant prend la main sans attendre la
// fin de la connexion).
func (e *Elector) Run(ctx context.Context) {
	every := e.Every
	if every <= 0 {
		every = Every
	}
	e.Tick(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			e.release()
			return
		case <-t.C:
			e.Tick(ctx)
		}
	}
}

// Tick : un tour d'élection. Tient le verrou s'il est libre, vérifie qu'il est toujours tenu sinon.
func (e *Elector) Tick(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	qctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if e.conn == nil {
		c, err := e.DB.Conn(qctx)
		if err != nil {
			e.lose("connexion", err)
			return
		}
		e.conn = c
	}
	if e.held.Load() {
		var n int
		err := e.conn.QueryRowContext(qctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND classid = 0 AND objid = $1
			AND objsubid = 1 AND pid = pg_backend_pid() AND granted`, Key).Scan(&n)
		if err != nil || n == 0 {
			e.lose("verrou perdu", err)
		}
		return
	}
	var ok bool
	if err := e.conn.QueryRowContext(qctx, `SELECT pg_try_advisory_lock($1)`, Key).Scan(&ok); err != nil {
		e.lose("prise du verrou", err)
		return
	}
	if ok {
		e.held.Store(true)
		e.log().Info("leader : cet exemplaire mène l'envoi, la relève IMAP, les webhooks et le certificat du relais")
	}
}

// lose : cesse de mener et jette la connexion (le verrou part avec elle côté PostgreSQL).
func (e *Elector) lose(why string, err error) {
	was := e.held.Swap(false)
	if e.conn != nil {
		_ = e.conn.Close()
		e.conn = nil
	}
	if was {
		l := e.log().WithField("cause", why)
		if err != nil {
			l = l.WithField("error", err.Error())
		}
		l.Warn("leader : cet exemplaire ne mène plus")
	}
}

func (e *Elector) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.held.Load() && e.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = e.conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, Key)
		cancel()
	}
	e.lose("arrêt", nil)
}

func (e *Elector) log() logger.Logger {
	if e.Log == nil {
		return logger.NewLoggerWithLevel("disabled")
	}
	return e.Log
}
