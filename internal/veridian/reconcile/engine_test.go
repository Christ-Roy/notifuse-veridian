package reconcile

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeInv struct {
	code  string
	num   int
	fs    []Finding
	err   error
	panic bool
}

func (f fakeInv) Code() string  { return f.code }
func (f fakeInv) Number() int   { return f.num }
func (f fakeInv) Title() string { return "fake " + f.code }
func (f fakeInv) Check(context.Context, Env) ([]Finding, error) {
	if f.panic {
		panic("boom")
	}
	return f.fs, f.err
}

func withRegistry(t *testing.T, invs ...Invariant) {
	saved := registry
	registry = append([]Invariant{}, invs...)
	t.Cleanup(func() { registry = saved })
}

func TestRunChecks_ThreeStatesNeverTwo(t *testing.T) {
	withRegistry(t,
		fakeInv{code: "propre", num: 1},
		fakeInv{code: "ecart", num: 2, fs: []Finding{{SubjectRef: "x", Severity: SeverityWarning, ProposedAction: &Action{Kind: "k"}}}},
		fakeInv{code: "casse", num: 3, err: errors.New("table absente")},
		fakeInv{code: "absent", num: 4, err: &ErrNotApplicable{Reason: "boîte unifiée pas encore migrée"}},
		fakeInv{code: "panique", num: 5, panic: true},
	)
	reports, found := RunChecks(context.Background(), Env{}, nil)
	status := map[string]CheckStatus{}
	for _, r := range reports {
		status[r.Check] = r.Status
	}
	assert.Equal(t, StatusOK, status["propre"])
	assert.Equal(t, StatusFindings, status["ecart"])
	assert.Equal(t, StatusIncomplet, status["casse"], "une erreur n'est jamais « ok »")
	assert.Equal(t, StatusIncomplet, status["absent"])
	assert.Equal(t, StatusIncomplet, status["panique"], "une panique n'est jamais « ok »")
	assert.Contains(t, reports[3].Error, "pas encore migrée")
	require.Len(t, found["ecart"], 1)
	assert.Equal(t, "ecart", found["ecart"][0].Check, "le code du contrôle est posé par le moteur")
	assert.Equal(t, "POST", found["ecart"][0].ProposedAction.API.Method, "chaque action porte son appel d'API")
	assert.Equal(t, "/api/veridian/reconcile.resolve", found["ecart"][0].ProposedAction.API.Path)
	_, hasCasse := found["casse"]
	assert.False(t, hasCasse, "un contrôle non abouti ne rend aucun verdict")
}

func TestRunChecks_OnlyFilterByCodeOrNumber(t *testing.T) {
	withRegistry(t, fakeInv{code: "a", num: 3}, fakeInv{code: "b", num: 6})
	reports, _ := RunChecks(context.Background(), Env{}, []string{"6"})
	require.Len(t, reports, 1)
	assert.Equal(t, "b", reports[0].Check)
	reports, _ = RunChecks(context.Background(), Env{}, []string{"a"})
	require.Len(t, reports, 1)
	reports, _ = RunChecks(context.Background(), Env{}, []string{"inconnu"})
	assert.Empty(t, reports)
}

func TestRegistry_RealInvariants(t *testing.T) {
	invs := Invariants()
	nums := map[int]string{}
	codes := map[string]bool{}
	for _, i := range invs {
		assert.NotEmpty(t, i.Title())
		assert.Regexp(t, regexp.MustCompile(`^[a-z_]+$`), i.Code())
		assert.False(t, codes[i.Code()], "code unique : %s", i.Code())
		codes[i.Code()] = true
		_, dup := nums[i.Number()]
		assert.False(t, dup, "numéro unique : %d", i.Number())
		nums[i.Number()] = i.Code()
	}
	for _, n := range []int{3, 4, 6, 7} {
		assert.Contains(t, nums, n, "invariant %d livré", n)
	}
	for i := 1; i < len(invs); i++ {
		assert.Less(t, invs[i-1].Number(), invs[i].Number(), "ordre des numéros")
	}
	if inv, ok := FindInvariant("6"); assert.True(t, ok) {
		assert.Equal(t, "classes", inv.Code())
	}
}

func TestFrInt(t *testing.T) {
	assert.Equal(t, "0", frInt(0))
	assert.Equal(t, "999", frInt(999))
	assert.Equal(t, "1 218", frInt(1218))
	assert.Equal(t, "1 234 567", frInt(1234567))
	assert.Equal(t, "-1 000", frInt(-1000))
}

func TestDefinitions_OneByQuantity(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Definitions() {
		assert.False(t, seen[d.Key], "une seule définition par grandeur : %s", d.Key)
		seen[d.Key] = true
		assert.NotEmpty(t, d.Definition)
	}
	for _, k := range []string{"inscrits", "envoyes_etape", "reponses_humaines", "stock_restant"} {
		assert.True(t, seen[k], k)
	}
}
