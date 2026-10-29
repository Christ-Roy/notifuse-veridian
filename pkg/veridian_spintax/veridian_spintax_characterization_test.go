package veridian_spintax

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests de caractérisation du spintax (lot L4, 29/10/2026), écrits depuis les mutants qui
// survivaient aux tests d'origine : bornes de profondeur, accolades en fin de texte, contenu
// Liquid qui porte lui-même des accolades ou des barres verticales. Le spintax tourne sur
// chaque e-mail de prospection : une erreur ici casse tous les envois d'une campagne.

func seeds(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("prospect%d@example.com", i)
	}
	return out
}

// assertOnlyAndAll vérifie que, sur de nombreux seeds, la sortie reste dans l'ensemble attendu
// et que chaque valeur de l'ensemble apparaît au moins une fois.
func assertOnlyAndAll(t *testing.T, in string, want ...string) {
	t.Helper()
	seen := map[string]bool{}
	allowed := map[string]bool{}
	for _, w := range want {
		allowed[w] = true
	}
	for _, s := range seeds(80) {
		out := ResolveSpintax(in, s)
		require.True(t, allowed[out], "sortie inattendue %q pour %q", out, in)
		seen[out] = true
	}
	for _, w := range want {
		assert.True(t, seen[w], "l'option %q n'a jamais été choisie sur 80 seeds", w)
	}
}

func TestResolveSpintax_LiquidAvecAccoladeDansUneChaine(t *testing.T) {
	// La « } » de la chaîne Liquid ne ferme pas le groupe spintax.
	assertOnlyAndAll(t, `{A {{ '}' }}|B}`, `A {{ '}' }}`, `B`)
}

func TestResolveSpintax_LiquidTagAvecBarreEtAccolade(t *testing.T) {
	assertOnlyAndAll(t, `{A {% if x == '}' %}y{% endif %}|B}`, `A {% if x == '}' %}y{% endif %}`, `B`)
	// La barre d'un tag Liquid ne sépare pas les options.
	assertOnlyAndAll(t, `{A {% if x == 'p|q' %}y{% endif %}|B}`, `A {% if x == 'p|q' %}y{% endif %}`, `B`)
}

func TestResolveSpintax_LiquidApresLeGroupeResteIntact(t *testing.T) {
	// Après le groupe, la position de lecture doit retomber juste après la fermeture : un texte
	// qui suit, Liquid compris, est recopié tel quel.
	for _, s := range seeds(20) {
		out := ResolveSpintax(`{a|b} puis {{ x }} et {% if y %}z{% endif %}`, s)
		assert.True(t, out == `a puis {{ x }} et {% if y %}z{% endif %}` || out == `b puis {{ x }} et {% if y %}z{% endif %}`, out)
	}
}

func TestResolveSpintax_AccoladesEnFinDeTexte(t *testing.T) {
	for _, in := range []string{"fin {", "fin {{", "fin {%", "{", "{{", "{%", "a{b", "{a|b", "{a|b}{", "{a|b}{{", "{a|b}{%"} {
		require.NotPanics(t, func() { _ = ResolveSpintax(in, "s") }, in)
	}
	assert.Equal(t, "fin {", ResolveSpintax("fin {", "s"))
	assert.Equal(t, "fin {{", ResolveSpintax("fin {{", "s"))
	assert.Equal(t, "fin {%", ResolveSpintax("fin {%", "s"))
	// Une accolade orpheline à la fin d'une option ne perturbe pas le groupe qui précède.
	out := ResolveSpintax("{a|b} {", "s")
	assert.True(t, out == "a {" || out == "b {", out)
}

func TestParseInto_GardeDeProfondeur(t *testing.T) {
	// Au-delà de maxNestingDepth, le reste est recopié tel quel plutôt que résolu ; à la limite exacte, il est résolu.
	in := "{a|b}"
	for _, tc := range []struct {
		depth    int
		resolved bool
	}{{0, true}, {maxNestingDepth, true}, {maxNestingDepth + 1, false}} {
		p := &parser{input: in, seedHash: hashSeed("s")}
		var b strings.Builder
		p.parseInto(&b, 0, len(in), tc.depth)
		if tc.resolved {
			assert.Contains(t, []string{"a", "b"}, b.String(), "profondeur %d", tc.depth)
		} else {
			assert.Equal(t, in, b.String(), "profondeur %d", tc.depth)
		}
	}
}

func TestCountVariants_LiquidAvecBarreOuAccoladeNeCompteRien(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`{A {% if x == 'p|q' %}y{% endif %}|B}`, 2},
		{`{A {{ '}' }}|B}`, 2},
		{`{A {{ x | upcase }}|B|C}`, 3},
		{`{A {% if x == '}' %}y{% endif %}|B}`, 2},
		{`{{ a | b }} {% if c | d %}e{% endif %}`, 1},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, CountVariants(c.in), c.in)
		// Cohérence avec la résolution : jamais plus de variantes rendues que comptées.
		uniq := map[string]bool{}
		for _, s := range seeds(60) {
			uniq[ResolveSpintax(c.in, s)] = true
		}
		assert.LessOrEqual(t, len(uniq), c.want, c.in)
	}
}

func TestCapAddCapMul(t *testing.T) {
	assert.Equal(t, 6, capMul(2, 3))
	assert.Equal(t, countVariantsCap, capMul(countVariantsCap, 2))
	assert.Equal(t, countVariantsCap, capMul(2, countVariantsCap))
	assert.Equal(t, countVariantsCap, capMul(40000, 40000), "produit au-dessus du plafond")
	assert.Equal(t, 5, capAdd(2, 3))
	assert.Equal(t, countVariantsCap, capAdd(countVariantsCap-1, 5))
	assert.Equal(t, countVariantsCap, capAdd(1<<62, 1<<62), "débordement d'entier ramené au plafond")
	// Un gros template ne dépasse jamais le plafond et ne panique pas.
	big := strings.Repeat("{a|b|c|d|e|f|g|h|i|j}", 40)
	assert.Equal(t, countVariantsCap, CountVariants(big))
}
