package veridian_spintax

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountVariants(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     int
	}{
		{"pas de spintax → 1", "Bonjour, comment allez-vous ?", 1},
		{"chaîne vide → 1", "", 1},
		{"un groupe à 2 options → 2", "{Bonjour|Salut}", 2},
		{"un groupe à 3 options → 3", "{Bonjour|Salut|Coucou}", 3},
		{"deux groupes top-level → produit 4", "{A|B} {C|D}", 4},
		{"trois groupes → 2*2*2 = 8", "{A|B}{C|D}{E|F}", 8},
		{"groupes asymétriques → 2*3 = 6", "{A|B} et {C|D|E}", 6},
		// nesting : option "Bonjour {Monsieur|Madame}" = 2 variantes, option
		// "Salut" = 1 → ce groupe vaut 3.
		{"nesting → somme des options (2+1=3)", "{Bonjour {Monsieur|Madame}|Salut}", 3},
		// nesting plus profond : {a|{b|c}} → option a (1) + option {b|c} (2) = 3.
		{"nesting interne → 1+2 = 3", "{a|{b|c}}", 3},
		// Liquid ignoré : ne compte pas comme spintax.
		{"Liquid var ignoré → 1", "Bonjour {{ contact.first_name }}", 1},
		{"Liquid tag ignoré → 1", "{% if x %}ok{% endif %}", 1},
		{"Liquid + spintax → seul le spintax compte", "{{ contact.name }}, {bonjour|salut}", 2},
		// {x} sans pipe = littéral, ne compte pas.
		{"accolade sans pipe = littéral → 1", "prix {final}", 1},
		// accolade déséquilibrée = best-effort, pas de panic, pas de variante.
		{"accolade ouverte non fermée → 1", "{bonjour", 1},
		// combinaison réaliste sujet cold.
		{"sujet cold réaliste → 2*2 = 4", "{Question|Idée} pour {votre équipe|vous}", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CountVariants(tt.template))
		})
	}
}

// TestCountVariants_ConsistentWithResolve vérifie que CountVariants compte bien
// le nombre de rendus DISTINCTS que ResolveSpintax peut produire : on énumère
// des seeds et on compte les rendus uniques, qui doit être ≤ CountVariants (et
// l'atteindre pour un petit template avec assez de seeds).
func TestCountVariants_ConsistentWithResolve(t *testing.T) {
	tpl := "{A|B|C} {X|Y}" // 3*2 = 6 variantes
	assert.Equal(t, 6, CountVariants(tpl))

	seen := map[string]struct{}{}
	for i := 0; i < 5000; i++ {
		out := ResolveSpintax(tpl, "seed"+string(rune(i)))
		seen[out] = struct{}{}
	}
	// On doit retrouver exactement les 6 variantes (assez de seeds pour couvrir).
	assert.Equal(t, 6, len(seen), "ResolveSpintax doit produire exactement CountVariants rendus distincts")
}

func TestCountVariants_NeverPanicsOnAdversarial(t *testing.T) {
	// best-effort : accolades pathologiques ne doivent jamais paniquer.
	adversarial := []string{
		"{{{{{{{{", "}}}}}}}}", "{a|{b|{c|{d|", "{|||}", "{}{}{}",
		"{a|b}{c", "{% {{ |{a|b}",
	}
	for _, s := range adversarial {
		assert.NotPanics(t, func() { _ = CountVariants(s) })
		assert.GreaterOrEqual(t, CountVariants(s), 1)
	}
}
