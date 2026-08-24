package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Le garde-fou DOIT être vu en train de refuser : un dispositif de sécurité qu'on
// n'a jamais observé se déclencher est réputé non fonctionnel.
func TestVeridianTemplateLabelLeak_RefuseIncidentBody(t *testing.T) {
	// Corps texte exactement tel qu'il est parti dans les 215 mails cold du
	// 2026-08-24 : le libellé interne du gabarit, puis la salutation.
	body := "Ouverture observation\nBonjour,\n\nJ'ai regardé votre boutique en ligne et j'ai relevé deux points concrets.\n\nRobert"

	leak := veridianTemplateLabelLeak(body)
	require.NotEmpty(t, leak, "le garde-fou devait REFUSER ce corps")
	assert.Equal(t, "Ouverture observation", leak)
}

func TestVeridianTemplateLabelLeak_Refuse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"libellé deux mots avant Bonjour",
			"Ouverture observation\nBonjour,\nLe corps du mail.",
			"Ouverture observation",
		},
		{
			"libellé un seul mot",
			"Relance\nBonjour Marie,\nSuite à notre échange.",
			"Relance",
		},
		{
			"libellé avec cadence J+3",
			"Relance J+3\nBonjour,\nUn message.",
			"Relance J+3",
		},
		{
			"libellé cinq mots (borne haute)",
			"Sequence cold ecom tier un\nBonjour,\nUn message.",
			"Sequence cold ecom tier un",
		},
		{
			"salutation anglaise",
			"Warm intro variant\nHi Jane,\nQuick note about your store.",
			"Warm intro variant",
		},
		{
			"salutation Cher/Madame",
			"Prise de contact\nChère Madame Dupont,\nJe me permets de vous écrire.",
			"Prise de contact",
		},
		{
			"lignes vides entre le libellé et la salutation",
			"Ouverture observation\n\n\nBonjour,\nLe corps.",
			"Ouverture observation",
		},
		{
			"CRLF (corps SMTP réel)",
			"Ouverture observation\r\nBonjour,\r\n\r\nLe corps.",
			"Ouverture observation",
		},
		{
			"salutation dans la fenêtre de lookahead",
			"Ouverture observation\nVeridian\nRobert Brunon\nBonjour,\nLe corps.",
			"Ouverture observation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := veridianTemplateLabelLeak(tc.body)
			assert.Equal(t, tc.want, got, "corps=%q", tc.body)
		})
	}
}

// Non-régression : tout ce qui est un vrai mail doit passer. Un garde-fou qui
// refuse du sain finit contourné, et le jour où il a raison personne ne l'écoute.
func TestVeridianTemplateLabelLeak_Accepte(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"vide", ""},
		{"espaces seuls", "   \n\n  \t "},
		{"salutation en première ligne", "Bonjour,\n\nJ'ai regardé votre site.\n\nRobert"},
		{"salutation avec prénom", "Bonjour Marie,\nJ'ai regardé votre site."},
		{"salutation anglaise", "Hi Jane,\nI had a look at your store."},
		{"salutation Liquid", "Bonjour {{ contact.first_name }},\nJ'ai regardé votre site."},
		{"première ligne purement Liquid", "{{ contact.first_name }},\nBonjour,\nJ'ai regardé votre site."},
		{"corps entier avec du Liquid", "Bonjour {{ contact.first_name | default: '' }},\n\nVotre boutique {{ contact.company }} fait {{ stats.visits }} visites.\n\n{% if contact.plan %}Vous êtes en plan {{ contact.plan }}.{% endif %}\n\nRobert"},
		{"phrase d'accroche longue", "Votre boutique en ligne perd des clients à l'étape du panier\nBonjour,\nJe m'explique."},
		{"première ligne ponctuée", "Une question rapide.\nBonjour,\nJe m'explique."},
		{"première ligne avec virgule", "Rapidement, deux points\nBonjour,\nJe m'explique."},
		{"libellé court mais AUCUNE salutation ensuite", "Ouverture observation\nCeci est un rapport interne sans salutation."},
		{"URL en première ligne", "https://veridian.site\nBonjour,\nLe corps."},
		{"email en première ligne", "robert@veridian.site\nBonjour,\nLe corps."},
		{"six mots en première ligne", "Ouverture observation ecom tier un bis\nBonjour,\nLe corps."},
		{"une seule ligne", "Ouverture observation"},
		{"salutation seule", "Bonjour,"},
		{"salutation lointaine hors lookahead", "Ouverture observation\nA\nB\nC\nD\nE\nBonjour,\nLe corps."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Empty(t, veridianTemplateLabelLeak(tc.body),
				"corps sain refusé à tort : %q", tc.body)
		})
	}
}

func TestVeridianLineIsGreeting(t *testing.T) {
	for _, l := range []string{"Bonjour,", "bonjour", "Bonsoir Jean", "Salut !", "Coucou", "Chère Madame", "Chere Madame", "Hi", "Hello there,", "Dear Sir", "Good morning,", "{{ contact.first_name }},"} {
		assert.True(t, veridianLineIsGreeting(l), "%q doit être vu comme une salutation", l)
	}
	for _, l := range []string{"", "Ouverture observation", "Votre boutique", "Robert Brunon"} {
		assert.False(t, veridianLineIsGreeting(l), "%q ne doit pas être vu comme une salutation", l)
	}
}

func TestVeridianCSSZeroHelpers(t *testing.T) {
	for _, v := range []string{"0", "0px", "0pt", "0.0", "0em", "0%"} {
		assert.True(t, veridianCSSLengthIsZero(v), "%q doit être une longueur nulle", v)
	}
	for _, v := range []string{"", "1px", "0.1em", "auto", "none", "inherit", "10"} {
		assert.False(t, veridianCSSLengthIsZero(v), "%q ne doit pas être une longueur nulle", v)
	}
	for _, v := range []string{"0", "0.0", "0%", ".0"} {
		assert.True(t, veridianCSSNumberIsZero(v), "%q doit être zéro", v)
	}
	for _, v := range []string{"", "1", "0.5", "inherit"} {
		assert.False(t, veridianCSSNumberIsZero(v), "%q ne doit pas être zéro", v)
	}
}

// === Veridian — discriminant d'origine (audit prod 2026-08-24) ===
//
// L'audit de tous les gabarits de tous les workspaces prod a montré que le
// garde-fou, appliqué sans discriminant au texte dérivé du HTML, aurait REFUSÉ
// 5 campagnes clientes saines : leur mail s'ouvre sur un en-tête de marque
// visible (« Céline Gaetan ») suivi de « Bonjour, » — signature textuelle
// identique à celle de l'incident. Ces tests figent la frontière.

// htmlBrandHeaderThenGreeting : en-tête de marque VISIBLE, puis salutation.
// Reproduit la structure MJML compilée des gabarits `celinegaetan/camp-*`.
const htmlBrandHeaderThenGreeting = `<!doctype html><html><head><title>camp-notaires</title></head><body>
<div style="background:#1E2A38;padding:30px 0"><div style="text-align:center;color:#C9A15A;font-size:26px">Céline Gaetan</div>
<div style="text-align:center;color:#E6DECF;font-size:12px">Conseillère en immobilier · Lyon</div></div>
<div style="background:#FFFFFF;padding:38px 32px"><div>Bonjour Marie,</div>
<div>Vos clients ont régulièrement besoin d'estimer un bien.</div></div>
</body></html>`

// htmlHiddenPreheaderThenGreeting : le préheader MJML masqué, tel que gomjml le
// compile — c'est l'incident du 2026-08-24.
const htmlHiddenPreheaderThenGreeting = `<!doctype html><html><head><title>Ouverture observation</title></head><body>
<div style="display:none;font-size:1px;color:#ffffff;line-height:1px;max-height:0px;max-width:0px;opacity:0;overflow:hidden;">Ouverture observation</div>
<div>Bonjour Marie,</div><div>Je suis tombé sur votre boutique.</div>
</body></html>`

func TestVeridianTextComesFromHiddenNode(t *testing.T) {
	t.Run("en-tête de marque visible → PAS une fuite", func(t *testing.T) {
		text := veridianHTMLToText(htmlBrandHeaderThenGreeting)
		leak := veridianTemplateLabelLeak(text)
		if leak != "Céline Gaetan" {
			t.Fatalf("le garde-fou seul devrait pointer l'en-tête (c'est justement l'ambiguïté à lever), obtenu %q", leak)
		}
		if veridianTextComesFromHiddenNode(leak, htmlBrandHeaderThenGreeting) {
			t.Errorf("un en-tête de marque VISIBLE ne doit pas être vu comme venant d'un nœud masqué — une campagne cliente saine serait bloquée")
		}
	})

	t.Run("préheader masqué → fuite reconnue", func(t *testing.T) {
		// Le correctif de fond retire déjà le nœud masqué du texte : la première
		// ligne redevient la salutation.
		text := veridianHTMLToText(htmlHiddenPreheaderThenGreeting)
		if got := strings.SplitN(text, "\n", 2)[0]; got != "Bonjour Marie," {
			t.Fatalf("le préheader masqué ne doit plus remonter dans le texte, 1re ligne = %q", got)
		}
		// Et si une technique de masquage échappait au filtre, l'origine reste
		// identifiable : le libellé est bien porté par un nœud non rendu.
		if !veridianTextComesFromHiddenNode("Ouverture observation", htmlHiddenPreheaderThenGreeting) {
			t.Errorf("le libellé du préheader doit être reconnu comme venant d'un nœud masqué")
		}
	})

	t.Run("libellé absent du HTML", func(t *testing.T) {
		if veridianTextComesFromHiddenNode("Relance J+3", htmlBrandHeaderThenGreeting) {
			t.Errorf("un libellé absent du HTML ne vient d'aucun nœud masqué")
		}
	})
}
