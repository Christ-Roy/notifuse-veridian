package folders

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readJSON(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, into))
}

// ParsePath doit rendre exactement ce que rendait parseSegmentPath de la console (reference
// generee par le module TypeScript lui-meme, y compris espaces Unicode et bords).
func TestParsePath_MemeResultatQueLaConsole(t *testing.T) {
	var golden []struct {
		In  string   `json:"in"`
		Out []string `json:"out"`
	}
	readJSON(t, "parse_golden.json", &golden)
	require.NotEmpty(t, golden)
	for _, g := range golden {
		assert.Equal(t, g.Out, ParsePath(g.In), "entree %q", g.In)
	}
}

// L'ordre naturel doit etre celui d'Intl.Collator('fr', numeric, base) de la console.
func TestNaturalCompare_MemeOrdreQueLaConsole(t *testing.T) {
	var pairs []struct {
		A   string `json:"a"`
		B   string `json:"b"`
		Cmp int    `json:"cmp"`
	}
	readJSON(t, "collate_golden.json", &pairs)
	require.NotEmpty(t, pairs)
	for _, p := range pairs {
		assert.Equal(t, p.Cmp, NaturalCompare(p.A, p.B), "%q vs %q", p.A, p.B)
	}
}

func TestLabelForEtCompatName(t *testing.T) {
	path := []string{"2 Cibles", "Animaleries"}
	assert.Equal(t, "P1", LabelFor("2 Cibles / Animaleries / P1", path))
	assert.Equal(t, "P1", LabelFor("2 cibles  /  ANIMALERIES  /  P1", path), "espaces et casse toleres")
	assert.Equal(t, "Autre / P1", LabelFor("Autre / P1", path), "prefixe different : nom entier")
	assert.Equal(t, "Seul", LabelFor("Seul", nil))
	assert.Equal(t, "A / B / C", CompatName([]string{"A", "B"}, "C"))
	assert.Equal(t, "C", CompatName(nil, "C"))
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"Priorite", "AC/DC", "Taille 1/2", "1 Priorité", "P1 · haut"} {
		assert.NoError(t, ValidateName("dossier", ok), ok)
	}
	for _, bad := range []string{"", "  ", " lead", "trail ", "A / B", "/ A", "A /", "a / b", "ctl\x07", strings.Repeat("x", 256)} {
		assert.Error(t, ValidateName("dossier", bad), "%q", bad)
	}
}
