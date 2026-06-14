package veridian_spintax

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// optionSet extrait les options top-level d'un groupe `{a|b|c}` simple (sans
// nesting) pour vérifier qu'une sortie résolue appartient bien à l'ensemble des
// variantes possibles.
func optionSet(s string) []string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	return strings.Split(s, "|")
}

func TestResolveSpintax_NoOp_StrictPassthrough(t *testing.T) {
	cases := []string{
		"",
		"Bonjour, ceci est un email tout à fait normal.",
		"<p>Aucun spintax ici.</p>",
		"Pas de pipe : {juste un texte entre accolades}",
		"Accolade isolée { sans rien",
		"Accolade fermante seule } perdue",
		"Email simple sans aucune accolade du tout — non-régression.",
		"100 % de texte, 0 spintax, 0 Liquid.",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			// Le no-op doit être strict, indépendant du seed.
			assert.Equal(t, in, ResolveSpintax(in, "anyone@example.com"))
			assert.Equal(t, in, ResolveSpintax(in, ""))
			assert.Equal(t, in, ResolveSpintax(in, "other@example.com"))
		})
	}
}

func TestResolveSpintax_SimpleGroup_ChoosesOneOption(t *testing.T) {
	in := "{Bonjour|Salut|Coucou}"
	opts := optionSet(in)
	out := ResolveSpintax(in, "alice@example.com")
	assert.Contains(t, opts, out, "la sortie doit être une des options")
}

func TestResolveSpintax_Deterministic_SameSeedSameOutput(t *testing.T) {
	in := "{Bonjour|Salut|Coucou} {Monsieur|Madame}, voici {une offre|une proposition}."
	seed := "robert@veridian.site"
	first := ResolveSpintax(in, seed)
	// Re-render répété : doit être strictement identique (idempotence par seed).
	for i := 0; i < 50; i++ {
		assert.Equal(t, first, ResolveSpintax(in, seed), "même seed → même sortie à chaque appel")
	}
}

func TestResolveSpintax_DifferentSeeds_CanDiffer(t *testing.T) {
	// Avec assez d'options et de seeds, on doit observer au moins deux variantes
	// distinctes (sinon le resolver ne varie rien et le but cold est manqué).
	in := "{A|B|C|D|E|F|G|H}"
	seen := map[string]bool{}
	seeds := []string{
		"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com",
		"f@x.com", "g@x.com", "h@x.com", "i@x.com", "j@x.com",
	}
	for _, s := range seeds {
		seen[ResolveSpintax(in, s)] = true
	}
	assert.Greater(t, len(seen), 1, "des destinataires différents doivent recevoir des variantes différentes")
}

func TestResolveSpintax_Nesting(t *testing.T) {
	in := "{Bonjour {Monsieur|Madame}|Salut}"
	// Variantes possibles complètes après résolution.
	possible := map[string]bool{
		"Bonjour Monsieur": true,
		"Bonjour Madame":   true,
		"Salut":            true,
	}
	// Plusieurs seeds pour exercer les deux branches du groupe externe.
	for _, s := range []string{"1@x", "2@x", "3@x", "4@x", "5@x", "6@x", "7@x", "8@x"} {
		out := ResolveSpintax(in, s)
		assert.True(t, possible[out], "sortie inattendue: %q", out)
	}
}

func TestResolveSpintax_DeepNesting_NoLeakedBraces(t *testing.T) {
	// Nesting profond NON ambigu : les groupes imbriqués sont séparés par un
	// caractère (ici un préfixe) pour ne jamais coller deux `{` (ce qui serait
	// interprété comme une var Liquid — cf. TestResolveSpintax_DoubleBraceIsLiquidNotNestedSpintax).
	// Chaque branche se réduit à "P" + une lettre a..h.
	in := "P{ {a|b}| {c|d}| {e|f}| {g|h}}"
	for _, s := range []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10"} {
		out := ResolveSpintax(in, s)
		// "P" + un espace résiduel éventuel + une lettre : on vérifie l'absence
		// totale d'accolade ou de pipe résiduel et qu'une lettre a..h est présente.
		assert.NotContains(t, out, "{", "aucune accolade résiduelle: %q", out)
		assert.NotContains(t, out, "}", "aucune accolade résiduelle: %q", out)
		assert.NotContains(t, out, "|", "aucun pipe résiduel: %q", out)
		assert.Regexp(t, `^P\s?[a-h]$`, out, "branche résolue inattendue: %q", out)
	}
}

func TestResolveSpintax_DoubleBraceIsLiquidNotNestedSpintax(t *testing.T) {
	// DÉCISION DE DESIGN : `{{` collé est TOUJOURS interprété comme l'ouverture
	// d'une variable Liquid, JAMAIS comme deux groupes spintax imbriqués. C'est
	// la convention universelle (Liquid prime, non-régression absolue des `{{ }}`).
	// Pour imbriquer du spintax, l'auteur sépare les accolades : `{ {a|b}|c}`.
	in := "{{ contact.first_name }}"
	assert.Equal(t, in, ResolveSpintax(in, "x@y.com"),
		"une var Liquid `{{ }}` doit passer intacte, même si elle ressemble à un nesting")
}

func TestResolveSpintax_PreservesLiquidVariables(t *testing.T) {
	// Une variable Liquid hors spintax ne doit jamais être altérée.
	in := "Bonjour {{ contact.first_name }}, {bienvenue|bonjour} chez nous."
	out := ResolveSpintax(in, "x@y.com")
	assert.Contains(t, out, "{{ contact.first_name }}", "la variable Liquid doit rester intacte")
	assert.True(t,
		strings.Contains(out, "bienvenue") || strings.Contains(out, "bonjour"),
		"le groupe spintax doit être résolu: %q", out)
	// Pas d'accolade simple spintax résiduelle.
	assert.NotContains(t, out, "|")
}

func TestResolveSpintax_PreservesLiquidTags(t *testing.T) {
	in := "{% if contact.vip %}VIP{% endif %} {offre|promo}"
	out := ResolveSpintax(in, "z@y.com")
	assert.Contains(t, out, "{% if contact.vip %}", "le tag Liquid d'ouverture doit rester intact")
	assert.Contains(t, out, "{% endif %}", "le tag Liquid de fermeture doit rester intact")
	assert.True(t, strings.Contains(out, "offre") || strings.Contains(out, "promo"))
}

func TestResolveSpintax_LiquidVarNestedInsideSpintaxOption(t *testing.T) {
	// Cas explicitement demandé : une variable Liquid imbriquée dans une option.
	in := "{Bonjour {{contact.first_name}}|Salut}"
	possible := map[string]bool{
		"Bonjour {{contact.first_name}}": true,
		"Salut":                          true,
	}
	for _, s := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		out := ResolveSpintax(in, s)
		assert.True(t, possible[out], "sortie inattendue: %q", out)
	}
}

func TestResolveSpintax_LiquidPipeNotTreatedAsSpintaxSeparator(t *testing.T) {
	// Le `|` d'un filtre Liquid est DANS un bloc `{{ }}` → il ne doit pas être vu
	// comme un séparateur spintax. Et il n'y a aucune accolade simple ici, donc
	// no-op strict.
	in := "Salut {{ contact.first_name | upcase }} !"
	out := ResolveSpintax(in, "p@q.com")
	assert.Equal(t, in, out, "un filtre Liquid avec | ne doit pas déclencher de spintax")
}

func TestResolveSpintax_LiquidFilterPipeInsideSpintaxOption(t *testing.T) {
	// Le filtre `| upcase` est protégé par `{{ }}` ; le seul `|` spintax est
	// celui de niveau top entre les deux options.
	in := "{Bonjour {{ name | upcase }}|Salut}"
	possible := map[string]bool{
		"Bonjour {{ name | upcase }}": true,
		"Salut":                       true,
	}
	for _, s := range []string{"s1", "s2", "s3", "s4", "s5", "s6"} {
		out := ResolveSpintax(in, s)
		assert.True(t, possible[out], "le | du filtre Liquid ne doit pas casser le split: %q", out)
	}
}

func TestResolveSpintax_EmptyOptionsAllowed(t *testing.T) {
	// `{|texte}` doit pouvoir produire la chaîne vide OU "texte".
	in := "Préfixe-{|suffixe}"
	possible := map[string]bool{
		"Préfixe-":        true,
		"Préfixe-suffixe": true,
	}
	for _, s := range []string{"a", "b", "c", "d", "e", "f"} {
		out := ResolveSpintax(in, s)
		assert.True(t, possible[out], "sortie inattendue pour option vide: %q", out)
	}
}

func TestResolveSpintax_MultipleGroups_IndependentResolution(t *testing.T) {
	in := "{A|B} milieu {X|Y}"
	out := ResolveSpintax(in, "indep@x.com")
	// Structure: <A|B> espace milieu espace <X|Y>
	require.Regexp(t, `^[AB] milieu [XY]$`, out)
}

func TestResolveSpintax_RobustnessUnbalancedBraces_NoPanic(t *testing.T) {
	// Aucune de ces entrées ne doit paniquer. On vérifie surtout l'absence de
	// crash ; la sortie best-effort est juste "non vide / cohérente".
	cases := []string{
		"{Bonjour|Salut",           // groupe non fermé
		"Bonjour|Salut}",           // fermeture sans ouverture
		"{{ unclosed liquid var",   // Liquid var non fermée
		"{% unclosed liquid tag",   // Liquid tag non fermé
		"{a|{b|c}",                 // sous-groupe ferme le compte trop tôt
		"}{}{}{",                   // accolades en vrac
		"{a|b}}",                   // une fermante en trop
		"{{a|b}|c",                 // ambiguïté ouverture
		strings.Repeat("{a|b}", 0), // vide
		"{",
		"}",
		"|",
		"{{",
		"%}",
		"{%",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			assert.NotPanics(t, func() {
				_ = ResolveSpintax(in, "robust@x.com")
				_ = ResolveSpintax(in, "")
			})
		})
	}
}

func TestResolveSpintax_UnbalancedClosingBracePreservedAroundGroup(t *testing.T) {
	// Une `}` orpheline avant un vrai groupe ne doit pas être mangée et ne doit
	// pas empêcher la résolution du groupe valide qui suit.
	in := "texte } {A|B}"
	out := ResolveSpintax(in, "orphan@x.com")
	require.Regexp(t, `^texte \} [AB]$`, out)
}

func TestResolveSpintax_FastPathNoBrace(t *testing.T) {
	// Le fast-path (pas de `{`) doit renvoyer exactement l'entrée (identité).
	in := "Pas la moindre accolade dans cette ligne, donc identité parfaite."
	assert.Equal(t, in, ResolveSpintax(in, "fast@x.com"))
}

func TestResolveSpintax_GroupWithoutPipeIsLiteral(t *testing.T) {
	// `{texte}` sans `|` n'est pas du spintax → accolades préservées telles
	// quelles (un éditeur peut légitimement écrire `{0}` ou `{foo}`).
	in := "Code: {ABC} fin"
	assert.Equal(t, in, ResolveSpintax(in, "lit@x.com"))
}

func TestResolveSpintax_RealisticColdEmail(t *testing.T) {
	in := "{Bonjour|Salut} {{ contact.first_name }},\n\n" +
		"{Je me permets de vous contacter|Je vous écris} car " +
		"{votre entreprise|votre société} {pourrait être intéressée par|gagnerait à découvrir} " +
		"notre solution.\n\n{Cordialement|Bien à vous},\nRobert"

	out := ResolveSpintax(in, "prospect@bigcorp.fr")

	// Plus aucune accolade simple spintax (pas de `|` résiduel hors Liquid).
	assert.NotContains(t, out, "|", "le | filtré ici n'apparaît pas dans le contenu, doit avoir disparu")
	// La variable Liquid survit pour le rendu Liquid ultérieur.
	assert.Contains(t, out, "{{ contact.first_name }}")
	// Déterminisme.
	assert.Equal(t, out, ResolveSpintax(in, "prospect@bigcorp.fr"))
}

func TestResolveSpintax_SeedEmptyIsStable(t *testing.T) {
	in := "{A|B|C}"
	out := ResolveSpintax(in, "")
	assert.Equal(t, out, ResolveSpintax(in, ""), "seed vide doit rester déterministe")
	assert.Contains(t, optionSet(in), out)
}

func TestPickOption_BoundsAndDeterminism(t *testing.T) {
	h := hashSeed("seed@x.com")
	for groupIdx := uint64(0); groupIdx < 100; groupIdx++ {
		for n := 1; n <= 10; n++ {
			c := pickOption(h, groupIdx, n)
			require.GreaterOrEqual(t, c, 0)
			require.Less(t, c, n)
			// Déterminisme strict.
			assert.Equal(t, c, pickOption(h, groupIdx, n))
		}
	}
}

func TestPickOption_DistributionIsNotConstant(t *testing.T) {
	// Sur un même groupe (index fixe), des seeds variés doivent produire des
	// choix variés (pas tous identiques) — sinon pas de variation cold.
	const n = 5
	counts := make(map[int]int)
	for i := 0; i < 200; i++ {
		seed := string(rune('a'+i%26)) + string(rune('0'+i%10)) + "@x.com"
		c := pickOption(hashSeed(seed), 0, n)
		counts[c]++
	}
	assert.Greater(t, len(counts), 1, "le choix doit varier selon le seed")
}

func TestHashSeed_Deterministic(t *testing.T) {
	assert.Equal(t, hashSeed("a@x.com"), hashSeed("a@x.com"))
	assert.NotEqual(t, hashSeed("a@x.com"), hashSeed("b@x.com"))
}

func TestResolveSpintax_PathologicalDeepNesting_NoStackOverflow(t *testing.T) {
	// Construit `{x|{x|{x|...}}}` très profond (bien au-delà de maxNestingDepth)
	// pour vérifier le garde-fou : best-effort, jamais de panic/stack overflow.
	depth := 500
	in := strings.Repeat("{x|", depth) + "y" + strings.Repeat("}", depth)
	assert.NotPanics(t, func() {
		out := ResolveSpintax(in, "deep@x.com")
		assert.NotEmpty(t, out, "doit produire une sortie best-effort")
	})
}

func TestResolveSpintax_LongFlatContent_Performance(t *testing.T) {
	// Gros contenu plat avec quelques groupes : pas de panic, déterminisme tenu.
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("Lorem ipsum {dolor|sit|amet} ")
	}
	in := sb.String()
	out1 := ResolveSpintax(in, "perf@x.com")
	out2 := ResolveSpintax(in, "perf@x.com")
	assert.Equal(t, out1, out2, "déterminisme sur gros contenu")
	assert.NotContains(t, out1, "|", "tous les groupes résolus")
}
