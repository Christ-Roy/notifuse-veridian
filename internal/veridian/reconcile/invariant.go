package reconcile

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Querier : lecture seule sur la base du workspace (*sql.DB, *sql.Tx ou un faux en test).
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// StatsSource : les comptes bruts que sert le tableau de bord (le même code que
// prospection.stats). Le réconciliateur reconstruit le chiffre SERVI depuis ces comptes
// et le recoupe avec une requête indépendante.
type StatsSource interface {
	GetProspectionRaw(ctx context.Context, workspaceID string, since, until time.Time) (*domain.VeridianProspectionRaw, error)
}

// Env : ce qu'un invariant a le droit de toucher. Jamais d'écriture.
type Env struct {
	WorkspaceID string
	DB          Querier
	Stats       StatsSource
	Now         func() time.Time
	// Pause : délai entre deux lectures d'un contrôle qui confirme un écart (les
	// compteurs bougent pendant qu'on les lit). Zéro en test.
	Pause time.Duration
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Invariant : un contrôle. Check ne renvoie que des écarts (rien = conforme) ; une
// erreur signifie « non abouti », jamais « conforme ».
type Invariant interface {
	Code() string
	Number() int
	Title() string
	Check(ctx context.Context, env Env) ([]Finding, error)
}

// ErrNotApplicable : le contrôle ne peut pas conclure faute de donnée (table absente).
// Il est rendu « non abouti » avec cette raison.
type ErrNotApplicable struct{ Reason string }

func (e *ErrNotApplicable) Error() string { return e.Reason }

var registry []Invariant

// Register ajoute un invariant (appelé depuis l'init de son fichier).
func Register(i Invariant) {
	registry = append(registry, i)
	sort.SliceStable(registry, func(a, b int) bool { return registry[a].Number() < registry[b].Number() })
}

// Invariants rend les invariants connus, dans l'ordre de leur numéro.
func Invariants() []Invariant {
	out := make([]Invariant, len(registry))
	copy(out, registry)
	return out
}

// FindInvariant cherche par code ou par numéro ("classes", "6").
func FindInvariant(key string) (Invariant, bool) {
	for _, i := range registry {
		if i.Code() == key || fmt.Sprint(i.Number()) == key {
			return i, true
		}
	}
	return nil, false
}

// --- aides communes aux invariants ---

// plural choisit le singulier pour 0 et 1, le pluriel sinon (règle française).
func plural(n int, one, many string) string {
	if n <= 1 {
		return one
	}
	return many
}

// frInt formate un entier avec l'espace insécable des milliers (1 218 -> "1 218").
func frInt(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(" ")
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// sample garde les n premiers éléments (échantillon de preuve).
func sample(items []string, n int) []string {
	if len(items) <= n {
		out := make([]string, len(items))
		copy(out, items)
		return out
	}
	out := make([]string, n)
	copy(out, items[:n])
	return out
}

// resolveAPI : l'appel qui applique une action (même route pour toutes).
func resolveAPI(findingRef string) *ActionAPI {
	return &ActionAPI{Method: "POST", Path: "/api/veridian/reconcile.resolve", Body: map[string]any{
		"workspace_id": "<workspace_id>", "finding_id": findingRef, "action": "apply",
	}}
}

// bindAction rend une copie de l'action dont l'appel d'API désigne l'écart `id`.
func bindAction(a *Action, id string) *Action {
	if a == nil {
		return nil
	}
	c := *a
	api := ActionAPI{Method: "POST", Path: "/api/veridian/reconcile.resolve", Body: map[string]any{"finding_id": id, "action": "apply"}}
	if a.API != nil {
		api.Method, api.Path = a.API.Method, a.API.Path
	}
	c.API = &api
	return &c
}

// withAPI complète l'action avec son appel (l'identifiant de l'écart est posé à la persistance).
func withAPI(a *Action) *Action {
	if a != nil && a.API == nil {
		a.API = resolveAPI("<finding_id>")
	}
	return a
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
