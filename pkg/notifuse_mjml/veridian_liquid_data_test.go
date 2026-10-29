package notifuse_mjml

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Défaut de classe : une donnée de contact, de flux ou d'API qui contient de la
// syntaxe Liquid ne doit jamais être évaluée (GHSA v42 « Liquid inside contact
// data rendered as plain text »). Nos imports de contacts viennent de données
// scrapées non maîtrisées : un nom d'entreprise « {{ contact.email }} » ou une
// boucle « {% for %} » est du texte, pas du code.

// veridianLiquidTree : un mj-text qui affiche une variable, plus (optionnel) un
// bloc mj-liquid qui, lui, est du Liquid de l'auteur du modèle et doit rester évalué.
func veridianLiquidTree(text string, rawLiquid string) EmailBlock {
	textBase := NewBaseBlock("text-1", MJMLComponentMjText)
	textBase.Content = stringPtr(text)
	textBlock := &MJTextBlock{BaseBlock: textBase}

	columnBlock := &MJColumnBlock{BaseBlock: NewBaseBlock("column-1", MJMLComponentMjColumn)}
	columnBlock.Children = []EmailBlock{textBlock}

	sectionBlock := &MJSectionBlock{BaseBlock: NewBaseBlock("section-1", MJMLComponentMjSection)}
	sectionBlock.Children = []EmailBlock{columnBlock}

	bodyBlock := &MJBodyBlock{BaseBlock: NewBaseBlock("body-1", MJMLComponentMjBody)}
	bodyBlock.Children = []EmailBlock{sectionBlock}
	if rawLiquid != "" {
		lb := NewBaseBlock("liquid-1", MJMLComponentMjLiquid)
		lb.Content = stringPtr(rawLiquid)
		bodyBlock.Children = append(bodyBlock.Children, &MJLiquidBlock{BaseBlock: lb})
	}

	mjml := &MJMLBlock{BaseBlock: NewBaseBlock("mjml-1", MJMLComponentMjml)}
	mjml.Children = []EmailBlock{bodyBlock}
	return mjml
}

func veridianCompileHTML(t *testing.T, req CompileTemplateRequest) string {
	t.Helper()
	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "compilation: %+v", resp.Error)
	require.NotNil(t, resp.HTML)
	return *resp.HTML
}

func TestVeridianLiquidDonneeContactRestePleinTexte_EditeurVisuel(t *testing.T) {
	// L'expression est affichée telle quelle, jamais évaluée : ni l'email du
	// contact, ni le secret voisin, ni la boucle.
	cas := map[string]string{
		"expression":      "{{ contact.email }}",
		"secret voisin":   "{{ secret_token }}",
		"boucle":          "{% for i in (1..3) %}B{{ i }}{% endfor %}",
		"assign":          "{% assign x = 1 %}{{ x }}",
		"balise isolée":   "{%",
		"accolade seule":  "{{",
		"commentaire":     "{# note #}",
		"texte accolades": "Dupont {SA} fils",
	}
	for nom, valeur := range cas {
		t.Run(nom, func(t *testing.T) {
			html := veridianCompileHTML(t, CompileTemplateRequest{
				WorkspaceID:      "ws",
				MessageID:        "msg",
				VisualEditorTree: veridianLiquidTree("Bonjour {{ contact.company }}", ""),
				TemplateData: MapOfAny{
					"contact":      map[string]interface{}{"company": valeur, "email": "victime@example.com"},
					"secret_token": "SECRET-A-NE-PAS-FUIR",
				},
				TrackingSettings: TrackingSettings{EnableTracking: false},
			})
			assert.NotContains(t, html, "SECRET-A-NE-PAS-FUIR", "la donnée a été évaluée par Liquid")
			assert.NotContains(t, html, "victime@example.com", "la donnée a été évaluée par Liquid")
			assert.NotContains(t, html, "", "sentinelle non restaurée")
			assert.NotContains(t, html, "", "sentinelle non restaurée")
			// La valeur apparaît telle quelle : évaluée, elle aurait disparu ou changé.
			assert.Contains(t, html, valeur)
		})
	}
}

func TestVeridianLiquidDonneeContactRestePleinTexte_ModeCode(t *testing.T) {
	src := `<mjml><mj-body><mj-section><mj-column><mj-text>Bonjour {{ contact.company }}</mj-text></mj-column></mj-section></mj-body></mjml>`
	html := veridianCompileHTML(t, CompileTemplateRequest{
		WorkspaceID: "ws",
		MessageID:   "msg",
		MjmlSource:  &src,
		TemplateData: MapOfAny{
			"contact":      map[string]interface{}{"company": "{{ secret_token }}"},
			"secret_token": "SECRET-A-NE-PAS-FUIR",
		},
		TrackingSettings: TrackingSettings{EnableTracking: false},
	})
	assert.NotContains(t, html, "SECRET-A-NE-PAS-FUIR")
	assert.Contains(t, html, "{{ secret_token }}")
}

func TestVeridianLiquidDuModeleResteEvalue(t *testing.T) {
	// Garde inverse : ce qui est écrit par l'auteur du modèle (bloc mj-liquid,
	// expressions du texte) garde sa valeur. Un correctif qui coupe tout Liquid passerait le test précédent.
	html := veridianCompileHTML(t, CompileTemplateRequest{
		WorkspaceID:      "ws",
		MessageID:        "msg",
		VisualEditorTree: veridianLiquidTree("Bonjour {{ contact.first_name }}", `<mj-section><mj-column><mj-text>{% for i in (1..2) %}LIGNE{{ i }} {% endfor %}</mj-text></mj-column></mj-section>`),
		TemplateData:     MapOfAny{"contact": map[string]interface{}{"first_name": "Léa"}},
		TrackingSettings: TrackingSettings{EnableTracking: false},
	})
	assert.Contains(t, html, "Bonjour Léa")
	assert.Contains(t, html, "LIGNE1")
	assert.Contains(t, html, "LIGNE2")
}

func TestVeridianSpintaxNeTouchePasLesDonneesContact(t *testing.T) {
	// Le spintax est une syntaxe du modèle. Une donnée « {SARL|SAS} » reste telle quelle
	// alors que le spintax du modèle est résolu.
	html := veridianCompileHTML(t, CompileTemplateRequest{
		WorkspaceID:         "ws",
		MessageID:           "msg",
		VisualEditorTree:    veridianLiquidTree("{Bonjour|Salut} {{ contact.company }}", ""),
		TemplateData:        MapOfAny{"contact": map[string]interface{}{"company": "Dupont {SARL|SAS}"}},
		VeridianSpintaxSeed: "prospect@example.com",
		TrackingSettings:    TrackingSettings{EnableTracking: false},
	})
	assert.Contains(t, html, "Dupont {SARL|SAS}")
	assert.False(t, strings.Contains(html, "{Bonjour|Salut}"), "le spintax du modèle doit être résolu")
}

func TestVeridianAccoladeFermanteDeDonneeNeFermePasUnGroupeSpintax(t *testing.T) {
	// Une donnée « Léa} » placée dans un groupe {Salut Léa}|Bonjour} ne doit pas fermer
	// le groupe avant l'heure : le groupe se résout en une des deux variantes entières.
	html := veridianCompileHTML(t, CompileTemplateRequest{
		WorkspaceID:         "ws",
		MessageID:           "msg",
		VisualEditorTree:    veridianLiquidTree("{Salut {{ contact.first_name }}|Bonjour}", ""),
		TemplateData:        MapOfAny{"contact": map[string]interface{}{"first_name": "Léa}"}},
		VeridianSpintaxSeed: "a@example.com",
		TrackingSettings:    TrackingSettings{EnableTracking: false},
	})
	assert.NotContains(t, html, "|Bonjour}", "le groupe spintax a été fermé par la donnée")
	assert.True(t, strings.Contains(html, "Salut Léa}") || strings.Contains(html, "Bonjour"), "une variante entière attendue")
}

func TestVeridianSujetDonneeContactPleinTexte(t *testing.T) {
	subject := "Offre pour {{ contact.company }}"
	resp, err := CompileTemplate(CompileTemplateRequest{
		WorkspaceID:         "ws",
		MessageID:           "msg",
		VisualEditorTree:    minimalTree(),
		Subject:             &subject,
		TemplateData:        MapOfAny{"contact": map[string]interface{}{"company": "{SARL|SAS}"}},
		VeridianSpintaxSeed: "x@example.com",
	})
	require.NoError(t, err)
	require.True(t, resp.Success)
	require.NotNil(t, resp.Subject)
	assert.Equal(t, "Offre pour {SARL|SAS}", *resp.Subject)
}

func TestVeridianNeutralizeDataCopieProfondeSansMuter(t *testing.T) {
	orig := MapOfAny{
		"a": "{{ x }}",
		"n": 42,
		"l": []interface{}{"{% y %}", map[string]interface{}{"k": "{z}"}},
		"s": []string{"{q}"},
		"m": map[string]string{"k": "{r}"},
	}
	out := veridianNeutralizeData(orig)
	assert.Equal(t, "{{ x }}", orig["a"], "l'entrée ne doit pas être modifiée")
	assert.Equal(t, 42, out["n"])
	assert.NotContains(t, out["a"], "{")
	assert.NotContains(t, out["l"].([]interface{})[0], "{")
	assert.NotContains(t, out["l"].([]interface{})[1].(map[string]interface{})["k"], "{")
	assert.NotContains(t, out["s"].([]string)[0], "{")
	assert.NotContains(t, out["m"].(map[string]string)["k"], "{")
	assert.Equal(t, "{{ x }}", veridianRestoreBraces(out["a"].(string)))
}

func TestVeridianRestoreBracesRattrapeLEncodageURL(t *testing.T) {
	// Un filtre url_encode applique ses règles aux sentinelles : on rend %7B / %7D.
	assert.Equal(t, "q=%7Bx%7D", veridianRestoreBraces("q=%EE%80%80x%EE%80%81"))
}
