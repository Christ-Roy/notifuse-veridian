package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DismissQuietPeriod : un écart écarté à la main ne revient pas avant ce délai.
const DismissQuietPeriod = 7 * 24 * time.Hour

// DBTX : ce dont le magasin a besoin (*sql.DB ou *sql.Tx).
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// BeginnerDB : une base capable d'ouvrir une transaction.
type BeginnerDB interface {
	DBTX
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// SyncOutcome : ce qu'une synchronisation a fait.
type SyncOutcome struct {
	Opened     int
	Refreshed  int
	AutoClosed int
	Muted      int // écarts masqués car écartés à la main récemment
}

const findingColumns = `id, "check", severity, subject_ref, title, expected, observed, explanation,
	evidence, proposed_action, first_seen_at, last_seen_at, resolved_at, COALESCE(resolved_by, ''), resolution`

// SyncFindings aligne la table sur le résultat d'UN contrôle abouti : les écarts vus
// sont créés ou rafraîchis, les écarts ouverts qui ne sont plus vus sont clos
// automatiquement (« l'écart a disparu »). Un contrôle non abouti n'appelle jamais ceci.
func SyncFindings(ctx context.Context, db BeginnerDB, check string, current []Finding, now time.Time) (SyncOutcome, error) {
	var out SyncOutcome
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	open := map[string]string{} // subject_ref -> id
	rows, err := tx.QueryContext(ctx, `SELECT id, subject_ref FROM veridian_reconcile_finding WHERE "check" = $1 AND resolved_at IS NULL`, check)
	if err != nil {
		return out, fmt.Errorf("read open findings: %w", err)
	}
	for rows.Next() {
		var id, ref string
		if err := rows.Scan(&id, &ref); err != nil {
			_ = rows.Close()
			return out, err
		}
		open[ref] = id
	}
	if err := rows.Close(); err != nil {
		return out, err
	}

	muted := map[string]bool{}
	mrows, err := tx.QueryContext(ctx, `SELECT DISTINCT subject_ref FROM veridian_reconcile_finding
		WHERE "check" = $1 AND resolved_at >= $2 AND resolution->>'how' = 'dismissed'`, check, now.Add(-DismissQuietPeriod))
	if err != nil {
		return out, fmt.Errorf("read dismissed findings: %w", err)
	}
	for mrows.Next() {
		var ref string
		if err := mrows.Scan(&ref); err != nil {
			_ = mrows.Close()
			return out, err
		}
		muted[ref] = true
	}
	if err := mrows.Close(); err != nil {
		return out, err
	}

	seen := map[string]bool{}
	for _, f := range current {
		seen[f.SubjectRef] = true
		ev, _ := json.Marshal(nonNilMap(f.Evidence))
		if id, ok := open[f.SubjectRef]; ok {
			act := marshalAction(bindAction(f.ProposedAction, id))
			if _, err := tx.ExecContext(ctx, `UPDATE veridian_reconcile_finding SET severity=$2, title=$3, expected=$4, observed=$5,
				explanation=$6, evidence=$7, proposed_action=$8, last_seen_at=$9 WHERE id=$1`,
				id, string(f.Severity), f.Title, f.Expected, f.Observed, f.Explanation, string(ev), nullJSON(act), now); err != nil {
				return out, fmt.Errorf("refresh finding: %w", err)
			}
			out.Refreshed++
			continue
		}
		if muted[f.SubjectRef] {
			out.Muted++
			continue
		}
		newID := uuid.NewString()
		act := marshalAction(bindAction(f.ProposedAction, newID))
		if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_reconcile_finding
			(id, "check", severity, subject_ref, title, expected, observed, explanation, evidence, proposed_action, first_seen_at, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
			newID, check, string(f.Severity), f.SubjectRef, f.Title, f.Expected, f.Observed, f.Explanation, string(ev), nullJSON(act), now); err != nil {
			return out, fmt.Errorf("insert finding: %w", err)
		}
		out.Opened++
	}
	for ref, id := range open {
		if seen[ref] {
			continue
		}
		res, _ := json.Marshal(map[string]any{"how": "disappeared", "note": "l'écart n'est plus constaté"})
		if _, err := tx.ExecContext(ctx, `UPDATE veridian_reconcile_finding SET resolved_at=$2, resolved_by='auto', resolution=$3 WHERE id=$1 AND resolved_at IS NULL`, id, now, string(res)); err != nil {
			return out, fmt.Errorf("auto-close finding: %w", err)
		}
		out.AutoClosed++
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// ListOptions filtre la lecture des écarts.
type ListOptions struct {
	Check           string
	IncludeResolved bool
	Limit           int
}

// ListFindings lit les écarts, les plus graves puis les plus récents d'abord.
func ListFindings(ctx context.Context, db DBTX, opt ListOptions) ([]Finding, error) {
	limit := opt.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + findingColumns + ` FROM veridian_reconcile_finding WHERE true`
	var args []any
	if opt.Check != "" {
		args = append(args, opt.Check)
		q += fmt.Sprintf(` AND "check" = $%d`, len(args))
	}
	if !opt.IncludeResolved {
		q += ` AND resolved_at IS NULL`
	}
	args = append(args, limit)
	q += fmt.Sprintf(` ORDER BY (resolved_at IS NOT NULL),
		CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
		last_seen_at DESC, id LIMIT $%d`, len(args))
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Finding{}
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ErrFindingNotFound : aucun écart avec cet identifiant.
var ErrFindingNotFound = errors.New("finding not found")

// GetFinding lit un écart par identifiant.
func GetFinding(ctx context.Context, db DBTX, id string) (*Finding, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+findingColumns+` FROM veridian_reconcile_finding WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrFindingNotFound
	}
	f, err := scanFinding(rows)
	if err != nil {
		return nil, err
	}
	return &f, rows.Err()
}

// CloseFinding marque un écart résolu (par l'action appliquée ou écarté à la main).
func CloseFinding(ctx context.Context, db DBTX, id, by string, resolution map[string]any, now time.Time) error {
	res, _ := json.Marshal(nonNilMap(resolution))
	r, err := db.ExecContext(ctx, `UPDATE veridian_reconcile_finding SET resolved_at=$2, resolved_by=$3, resolution=$4 WHERE id=$1 AND resolved_at IS NULL`, id, now, by, string(res))
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return fmt.Errorf("écart %s déjà résolu ou introuvable", id)
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanFinding(r rowScanner) (Finding, error) {
	var f Finding
	var sev string
	var ev, act, res []byte
	var resolvedAt sql.NullTime
	if err := r.Scan(&f.ID, &f.Check, &sev, &f.SubjectRef, &f.Title, &f.Expected, &f.Observed, &f.Explanation,
		&ev, &act, &f.FirstSeenAt, &f.LastSeenAt, &resolvedAt, &f.ResolvedBy, &res); err != nil {
		return f, err
	}
	f.Severity = Severity(sev)
	f.FirstSeenAt, f.LastSeenAt = f.FirstSeenAt.UTC(), f.LastSeenAt.UTC()
	if resolvedAt.Valid {
		t := resolvedAt.Time.UTC()
		f.ResolvedAt = &t
	}
	f.Evidence = map[string]any{}
	if len(ev) > 0 {
		_ = json.Unmarshal(ev, &f.Evidence)
	}
	if len(act) > 0 && string(act) != "null" {
		var a Action
		if json.Unmarshal(act, &a) == nil {
			f.ProposedAction = &a
		}
	}
	if len(res) > 0 && string(res) != "null" {
		_ = json.Unmarshal(res, &f.Resolution)
	}
	return f, nil
}

func marshalAction(a *Action) []byte {
	if a == nil {
		return nil
	}
	b, _ := json.Marshal(a)
	return b
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// nullJSON : un jsonb se passe en texte (un []byte partirait en bytea avec lib/pq).
func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
