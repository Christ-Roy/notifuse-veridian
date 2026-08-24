package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// === Incident préheader 2026-08-24 ===
//
// Le gabarit Notifuse `ecom-t1-a` (workspace coldtunnel) portait
// `<mj-preview>Ouverture observation</mj-preview>` — le préheader valait le NOM
// INTERNE du gabarit. gomjml compile un mj-preview en exactement ce div (cf.
// gomjml/mjml/components/head.go), premier nœud du <body>. Sans filtrage des
// nœuds invisibles, le libellé devenait la première ligne du text/plain, avant
// « Bonjour, » — parti dans 215 mails cold.
const veridianIncidentPreheaderHTML = `<!doctype html><html><head><title>ecom-t1-a</title></head><body>` +
	`<div style="display:none;font-size:1px;color:#ffffff;line-height:1px;max-height:0px;max-width:0px;opacity:0;overflow:hidden;">Ouverture observation</div>` +
	`<div style="background-color:#ffffff;"><table><tr><td>` +
	`<p>Bonjour,</p>` +
	`<p>J'ai regardé votre boutique en ligne et j'ai relevé deux points concrets.</p>` +
	`<p>Robert</p>` +
	`</td></tr></table></div></body></html>`

func TestVeridianHTMLToText_IncidentPreheaderNeverLeaks(t *testing.T) {
	got := veridianHTMLToText(veridianIncidentPreheaderHTML)

	assert.NotContains(t, got, "Ouverture observation",
		"le préheader (libellé interne du gabarit) ne doit JAMAIS remonter dans le text/plain")
	assert.NotContains(t, got, "ecom-t1-a", "le <title> ne doit pas remonter non plus")

	require.NotEmpty(t, got)
	firstLine := strings.SplitN(got, "\n", 2)[0]
	assert.Equal(t, "Bonjour,", firstLine,
		"la première ligne du corps texte doit être la salutation, sortie=%q", got)

	assert.Contains(t, got, "J'ai regardé votre boutique en ligne")
	assert.Contains(t, got, "Robert")

	// Le garde-fou ne doit rien avoir à refuser sur un texte ainsi assaini.
	assert.Empty(t, veridianTemplateLabelLeak(got))
}

func TestVeridianHTMLToText_InvisibleNodesSkipped(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{"display:none", `<div style="display:none;">Caché</div><p>Visible</p>`},
		{"display:none !important", `<div style="display:none !important">Caché</div><p>Visible</p>`},
		{"display: NONE majuscules", `<div style="DISPLAY: NONE">Caché</div><p>Visible</p>`},
		{"visibility:hidden", `<span style="visibility:hidden">Caché</span><p>Visible</p>`},
		{"visibility:collapse", `<table><tr style="visibility:collapse"><td>Caché</td></tr></table><p>Visible</p>`},
		{"opacity:0", `<div style="opacity:0">Caché</div><p>Visible</p>`},
		{"opacity:0.0", `<div style="opacity:0.0">Caché</div><p>Visible</p>`},
		{"opacity:0%", `<div style="opacity:0%">Caché</div><p>Visible</p>`},
		{"max-height:0", `<div style="max-height:0">Caché</div><p>Visible</p>`},
		{"max-height:0px", `<div style="max-height:0px">Caché</div><p>Visible</p>`},
		{"attribut hidden", `<div hidden>Caché</div><p>Visible</p>`},
		{"attribut hidden=\"\"", `<div hidden="">Caché</div><p>Visible</p>`},
		{"aria-hidden true", `<div aria-hidden="true">Caché</div><p>Visible</p>`},
		{"aria-hidden TRUE", `<div aria-hidden="TRUE">Caché</div><p>Visible</p>`},
		{"sous-arbre entier coupé", `<div style="display:none"><table><tr><td><span>Caché</span></td></tr></table></div><p>Visible</p>`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := veridianHTMLToText(tc.html)
			assert.NotContains(t, got, "Caché", "nœud invisible remonté dans le texte : %q", got)
			assert.Contains(t, got, "Visible", "le texte visible doit être conservé : %q", got)
		})
	}
}

// Non-régression : ce qui est VISIBLE doit le rester. Une opacité partielle, une
// hauteur max non nulle, un aria-hidden="false", un font-size:0 de gouttière
// (hack courant en mail HTML dont les enfants redéfinissent leur taille) ne
// doivent jamais faire disparaître du texte affiché.
func TestVeridianHTMLToText_VisibleNodesPreserved(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{"opacity:0.5", `<div style="opacity:0.5">Visible</div>`},
		{"opacity:1", `<div style="opacity:1">Visible</div>`},
		{"max-height:100px", `<div style="max-height:100px">Visible</div>`},
		{"display:block", `<div style="display:block">Visible</div>`},
		{"visibility:visible", `<div style="visibility:visible">Visible</div>`},
		{"aria-hidden false", `<div aria-hidden="false">Visible</div>`},
		{"font-size:0 gouttière", `<div style="font-size:0px;line-height:0"><span style="font-size:14px">Visible</span></div>`},
		{"style sans déclaration masquante", `<div style="color:#333;padding:10px">Visible</div>`},
		{"attribut nommé hiddenfoo", `<div data-hiddenfoo="1">Visible</div>`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Contains(t, veridianHTMLToText(tc.html), "Visible",
				"du texte visible a été supprimé à tort")
		})
	}
}
