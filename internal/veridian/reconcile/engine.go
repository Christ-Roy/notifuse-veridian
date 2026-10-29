package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CheckTimeout borne chaque contrôle : un contrôle lent ne bloque pas les autres.
const CheckTimeout = 60 * time.Second

// RunChecks exécute les invariants demandés (tous si only est vide) et rend, pour
// chacun, son issue. Elle n'écrit rien : la persistance est à la charge de l'appelant
// (voir Engine.Run). Un invariant qui échoue ou panique est « non abouti », jamais « ok ».
func RunChecks(ctx context.Context, env Env, only []string) ([]CheckReport, map[string][]Finding) {
	selected := Invariants()
	if len(only) > 0 {
		want := map[string]bool{}
		for _, k := range only {
			want[k] = true
		}
		var sub []Invariant
		for _, inv := range selected {
			if want[inv.Code()] || want[fmt.Sprint(inv.Number())] {
				sub = append(sub, inv)
			}
		}
		selected = sub
	}
	reports := make([]CheckReport, 0, len(selected))
	found := map[string][]Finding{}
	for _, inv := range selected {
		rep := CheckReport{Check: inv.Code(), Number: inv.Number(), Title: inv.Title()}
		start := time.Now()
		fs, err := runOne(ctx, inv, env)
		rep.DurationMS = time.Since(start).Milliseconds()
		switch {
		case err != nil:
			rep.Status = StatusIncomplet
			rep.Error = err.Error()
		case len(fs) > 0:
			rep.Status = StatusFindings
			rep.Count = len(fs)
			found[inv.Code()] = fs
		default:
			rep.Status = StatusOK
			found[inv.Code()] = nil
		}
		reports = append(reports, rep)
	}
	return reports, found
}

func runOne(ctx context.Context, inv Invariant, env Env) (fs []Finding, err error) {
	defer func() {
		if r := recover(); r != nil {
			fs, err = nil, fmt.Errorf("panique dans le contrôle %s: %v", inv.Code(), r)
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	fs, err = inv.Check(cctx, env)
	if err != nil {
		var na *ErrNotApplicable
		if errors.As(err, &na) {
			return nil, na
		}
		return nil, err
	}
	for i := range fs {
		fs[i].Check = inv.Code()
		if fs[i].Evidence == nil {
			fs[i].Evidence = map[string]any{}
		}
		fs[i].ProposedAction = withAPI(fs[i].ProposedAction)
	}
	return fs, nil
}

// Engine : exécute les contrôles et tient la table des écarts à jour.
type Engine struct {
	DB   BeginnerDB
	Env  Env
	Time func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Time != nil {
		return e.Time().UTC()
	}
	return time.Now().UTC()
}

// Run exécute les contrôles demandés, synchronise la table (écarts ouverts, rafraîchis,
// clos car disparus) puis rend l'état des écarts ouverts. Un contrôle non abouti laisse
// ses écarts existants intacts.
func (e *Engine) Run(ctx context.Context, only []string) (*Report, error) {
	reports, found := RunChecks(ctx, e.Env, only)
	now := e.now()
	for _, r := range reports {
		if r.Status == StatusIncomplet {
			continue
		}
		fs := found[r.Check]
		if _, err := SyncFindings(ctx, e.DB, r.Check, fs, now); err != nil {
			return nil, fmt.Errorf("persist %s: %w", r.Check, err)
		}
	}
	rep, err := e.Report(ctx, "", false)
	if err != nil {
		return nil, err
	}
	rep.Ran = true
	rep.Checks = reports
	rep.Summary.NotReached = 0
	for _, r := range reports {
		if r.Status == StatusIncomplet {
			rep.Summary.NotReached++
		}
	}
	return rep, nil
}

// Report lit l'état des écarts sans rien exécuter.
func (e *Engine) Report(ctx context.Context, check string, includeResolved bool) (*Report, error) {
	fs, err := ListFindings(ctx, e.DB, ListOptions{Check: check, IncludeResolved: includeResolved, Limit: 500})
	if err != nil {
		return nil, err
	}
	for i := range fs {
		if a := fs[i].ProposedAction; a != nil && a.API != nil && e.Env.WorkspaceID != "" {
			a.API.Body["workspace_id"] = e.Env.WorkspaceID
		}
	}
	rep := &Report{GeneratedAt: e.now(), Findings: fs, Checks: []CheckReport{}, Definitions: Definitions()}
	for _, f := range fs {
		if f.ResolvedAt != nil {
			continue
		}
		rep.Summary.Open++
		switch f.Severity {
		case SeverityCritical:
			rep.Summary.Critical++
		case SeverityWarning:
			rep.Summary.Warning++
		default:
			rep.Summary.Info++
		}
	}
	return rep, nil
}
