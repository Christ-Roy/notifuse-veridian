package veridian

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

const trackingTable = `CREATE TABLE IF NOT EXISTS veridian_migrations (
	number INTEGER PRIMARY KEY,
	lot VARCHAR(8) NOT NULL,
	name VARCHAR(80) NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// verrou consultatif : deux instances qui démarrent ensemble (déploiement glissant) ne jouent pas la
// même migration en parallèle.
const advisoryLock = `SELECT pg_advisory_xact_lock(hashtext('veridian_migrations'))`

type scope int

const (
	scopeSystem scope = iota
	scopeWorkspace
)

// ApplySystem joue sur la base système les migrations manquantes. Renvoie les numéros joués.
func (r *Registry) ApplySystem(ctx context.Context, db *sql.DB) ([]int, error) {
	return r.apply(ctx, db, scopeSystem, "")
}

// ApplyWorkspace joue sur une base workspace les migrations manquantes. Renvoie les numéros joués.
func (r *Registry) ApplyWorkspace(ctx context.Context, db *sql.DB, workspaceID string) ([]int, error) {
	return r.apply(ctx, db, scopeWorkspace, workspaceID)
}

func (r *Registry) apply(ctx context.Context, db *sql.DB, sc scope, workspaceID string) ([]int, error) {
	specs := r.List()
	if len(specs) == 0 {
		return nil, nil
	}
	if _, err := db.ExecContext(ctx, trackingTable); err != nil {
		return nil, fmt.Errorf("create veridian_migrations: %w", err)
	}
	var played []int
	for _, s := range specs {
		ok, err := applyOne(ctx, db, s, sc, workspaceID)
		if err != nil {
			return played, fmt.Errorf("V%d %s: %w", s.Number, s.Name, err)
		}
		if ok {
			played = append(played, s.Number)
		}
	}
	return played, nil
}

// applyOne : une transaction par migration (verrou, test d'application, instructions, marque).
func applyOne(ctx context.Context, db *sql.DB, s Spec, sc scope, workspaceID string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, advisoryLock); err != nil {
		return false, fmt.Errorf("verrou: %w", err)
	}
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM veridian_migrations WHERE number = $1)`, s.Number).Scan(&done); err != nil {
		return false, fmt.Errorf("lecture du suivi: %w", err)
	}
	if done {
		return false, nil
	}
	stmts := s.System
	if sc == scopeWorkspace {
		stmts = s.Workspace
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return false, err
		}
	}
	if sc == scopeSystem && s.SystemFunc != nil {
		if err := s.SystemFunc(ctx, tx); err != nil {
			return false, err
		}
	}
	if sc == scopeWorkspace && s.WorkspaceFunc != nil {
		if err := s.WorkspaceFunc(ctx, workspaceID, tx); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_migrations (number, lot, name) VALUES ($1, $2, $3) ON CONFLICT (number) DO NOTHING`, s.Number, s.Lot, s.Name); err != nil {
		return false, fmt.Errorf("marque d'application: %w", err)
	}
	return true, tx.Commit()
}

// WorkspaceSource donne accès aux bases workspace (adaptateur fourni par le câblage).
type WorkspaceSource interface {
	IDs(ctx context.Context) ([]string, error)
	Conn(ctx context.Context, workspaceID string) (*sql.DB, error)
}

// Logger : sous-ensemble de pkg/logger utilisé ici.
type Logger interface {
	Info(msg string)
	Error(msg string)
}

// Failure : une base où une migration a échoué (l'application démarre quand même, voir RunAll).
type Failure struct {
	Scope string // "system" ou id du workspace
	Err   error
}

// Report : ce que RunAll a fait.
type Report struct {
	System     []int
	Workspaces map[string][]int
	Failures   []Failure
}

// RunAll joue les migrations manquantes sur la base système puis sur chaque workspace.
// Base système : toute erreur est fatale (même contrat que le gestionnaire upstream). Workspace :
// l'échec d'une base (introuvable, orpheline) est consigné dans Report.Failures et n'empêche ni les
// autres ni le démarrage ; il est journalisé en erreur et rejoué au prochain démarrage.
func (r *Registry) RunAll(ctx context.Context, sys *sql.DB, src WorkspaceSource, log Logger) (*Report, error) {
	rep := &Report{Workspaces: map[string][]int{}}
	played, err := r.ApplySystem(ctx, sys)
	if err != nil {
		return rep, fmt.Errorf("base système: %w", err)
	}
	rep.System = played
	ids, err := src.IDs(ctx)
	if err != nil {
		return rep, fmt.Errorf("liste des workspaces: %w", err)
	}
	sort.Strings(ids)
	for _, id := range ids {
		conn, err := src.Conn(ctx, id)
		if err == nil {
			var p []int
			p, err = r.ApplyWorkspace(ctx, conn, id)
			if len(p) > 0 {
				rep.Workspaces[id] = p
			}
		}
		if err != nil {
			rep.Failures = append(rep.Failures, Failure{Scope: id, Err: err})
			if log != nil {
				log.Error(fmt.Sprintf("veridian migrations: workspace %s: %v", id, err))
			}
		}
	}
	if log != nil {
		log.Info(fmt.Sprintf("veridian migrations: base système %v, %d workspace(s) migrés, %d échec(s)", rep.System, len(rep.Workspaces), len(rep.Failures)))
	}
	return rep, nil
}

// ApplyToNewWorkspace est appelé à la création d'une base workspace (database.EnsureWorkspaceDatabaseExists).
func ApplyToNewWorkspace(db *sql.DB, workspaceID string) error {
	_, err := Default.ApplyWorkspace(context.Background(), db, workspaceID)
	return err
}
