package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianHTMLToText(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains []string // sous-chaînes attendues dans la sortie
		excludes []string // sous-chaînes interdites dans la sortie
		empty    bool     // sortie attendue vide
	}{
		{
			name:  "vide → vide",
			html:  "",
			empty: true,
		},
		{
			name:  "espaces seuls → vide",
			html:  "   \n\t  ",
			empty: true,
		},
		{
			name:     "texte simple extrait",
			html:     "<p>Bonjour Jean</p>",
			contains: []string{"Bonjour Jean"},
		},
		{
			name:     "balises de formatage retirées, texte conservé",
			html:     "<p>On peut <strong>échanger</strong> cette <em>semaine</em> ?</p>",
			contains: []string{"On peut échanger cette semaine ?"},
		},
		{
			name:     "script et style jamais dans le texte",
			html:     "<style>.x{color:red}</style><script>var leak=42;</script><p>Visible</p>",
			contains: []string{"Visible"},
			excludes: []string{"color:red", "leak", "var leak=42"},
		},
		{
			name:  "uniquement script/style → vide (déclenche le fallback HTML-only)",
			html:  "<style>.x{color:red}</style><script>var a=1;</script>",
			empty: true,
		},
		{
			name:     "br produit un saut de ligne",
			html:     "Ligne1<br>Ligne2",
			contains: []string{"Ligne1\nLigne2"},
		},
		{
			name:     "entités HTML décodées",
			html:     "<p>Caf&eacute; &amp; cr&egrave;me</p>",
			contains: []string{"Café & crème"},
		},
		{
			name:     "blocs séparés par saut de ligne",
			html:     "<div>Paragraphe un</div><div>Paragraphe deux</div>",
			contains: []string{"Paragraphe un", "Paragraphe deux"},
		},
		{
			name:     "liste rendue en lignes",
			html:     "<ul><li>Item A</li><li>Item B</li></ul>",
			contains: []string{"Item A", "Item B"},
		},
		{
			name:     "espaces multiples effondrés",
			html:     "<p>Trop      d'espaces</p>",
			contains: []string{"Trop d'espaces"},
			excludes: []string{"Trop      d'espaces"},
		},
		{
			name:     "nbsp décodé puis normalisé en espace simple",
			html:     "<p>A&nbsp;&nbsp;B</p>",
			contains: []string{"A B"},
		},
		{
			name:     "document HTML complet (head ignoré, body extrait)",
			html:     "<html><head><title>Titre caché</title></head><body><p>Corps visible</p></body></html>",
			contains: []string{"Corps visible"},
			excludes: []string{"Titre caché"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := veridianHTMLToText(tc.html)
			if tc.empty {
				assert.Empty(t, got, "sortie attendue vide")
				return
			}
			for _, want := range tc.contains {
				assert.True(t, strings.Contains(got, want),
					"sortie %q doit contenir %q", got, want)
			}
			for _, no := range tc.excludes {
				assert.False(t, strings.Contains(got, no),
					"sortie %q ne doit pas contenir %q", got, no)
			}
		})
	}
}

// Pas de lignes vides multiples consécutives dans la sortie normalisée.
func TestVeridianHTMLToText_NoConsecutiveBlankLines(t *testing.T) {
	html := "<div>Un</div><br><br><br><div>Deux</div>"
	got := veridianHTMLToText(html)
	assert.NotContains(t, got, "\n\n\n", "pas plus d'une ligne vide consécutive")
	assert.Contains(t, got, "Un")
	assert.Contains(t, got, "Deux")
}

// Robustesse : un HTML malformé ne doit jamais paniquer (best-effort).
func TestVeridianHTMLToText_MalformedNoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		_ = veridianHTMLToText("<p>Ouvert sans fermer <div><span>texte")
		_ = veridianHTMLToText("<<<>>><p")
		_ = veridianHTMLToText("&notanentity; &#xZZZ;")
	})
}
