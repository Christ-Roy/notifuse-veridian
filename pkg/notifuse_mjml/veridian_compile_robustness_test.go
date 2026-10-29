package notifuse_mjml

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Robustesse de la compilation face à des DONNÉES de contact réelles : les noms d'entreprise
// scrapés contiennent « & », « < », des apostrophes. Un seul de ces caractères ne doit jamais
// faire échouer le rendu d'un e-mail (ce qui bloquerait l'envoi pour ce contact).
// Défaut révélé par les tests upstream v39.1 portés (TestCompileTemplateTreeMj*RendersAngleBrackets).

func veridianTreeWithHead(headTitle, headPreview, text string) EmailBlock {
	titleBase := NewBaseBlock("title-1", MJMLComponentMjTitle)
	titleBase.Content = stringPtr(headTitle)
	previewBase := NewBaseBlock("preview-1", MJMLComponentMjPreview)
	previewBase.Content = stringPtr(headPreview)
	head := &MJHeadBlock{BaseBlock: NewBaseBlock("head-1", MJMLComponentMjHead)}
	head.Children = []EmailBlock{&MJTitleBlock{BaseBlock: titleBase}, &MJPreviewBlock{BaseBlock: previewBase}}

	textBase := NewBaseBlock("text-1", MJMLComponentMjText)
	textBase.Content = stringPtr(text)
	col := &MJColumnBlock{BaseBlock: NewBaseBlock("column-1", MJMLComponentMjColumn)}
	col.Children = []EmailBlock{&MJTextBlock{BaseBlock: textBase}}
	sec := &MJSectionBlock{BaseBlock: NewBaseBlock("section-1", MJMLComponentMjSection)}
	sec.Children = []EmailBlock{col}
	body := &MJBodyBlock{BaseBlock: NewBaseBlock("body-1", MJMLComponentMjBody)}
	body.Children = []EmailBlock{sec}
	root := &MJMLBlock{BaseBlock: NewBaseBlock("mjml-1", MJMLComponentMjml)}
	root.Children = []EmailBlock{head, body}
	return root
}

func TestVeridianDonneeContactAvecCaracteresSpeciauxNeCasseJamaisLeRendu(t *testing.T) {
	valeurs := []string{"Durand & Fils", "Dupont <SARL>", "L'Atelier \"Zoé\"", "A&B > C", "R&D"}
	for _, v := range valeurs {
		for _, zone := range []string{"texte", "titre", "apercu"} {
			t.Run(zone+"/"+v, func(t *testing.T) {
				title, preview, text := "Offre", "Aperçu", "Bonjour"
				switch zone {
				case "texte":
					text = "Bonjour {{ contact.company }}"
				case "titre":
					title = "Offre pour {{ contact.company }}"
				case "apercu":
					preview = "Pour {{ contact.company }}"
				}
				resp, err := CompileTemplate(CompileTemplateRequest{
					WorkspaceID:      "ws",
					MessageID:        "msg",
					VisualEditorTree: veridianTreeWithHead(title, preview, text),
					TemplateData:     MapOfAny{"contact": map[string]interface{}{"company": v}},
					TrackingSettings: TrackingSettings{EnableTracking: false},
				})
				require.NoError(t, err)
				require.True(t, resp.Success, "le rendu doit réussir pour %q dans %s : %+v", v, zone, resp.Error)
				require.NotNil(t, resp.HTML)
				assert.False(t, strings.Contains(*resp.HTML, "{{"), "variable non rendue")
				require.NotNil(t, resp.MJML)
				assert.NotContains(t, *resp.MJML, "&amp;lt;", "double échappement : l'e-mail afficherait « &lt; » en clair")
				assert.NotContains(t, *resp.MJML, "&amp;#60;")
				if zone != "texte" && strings.Contains(v, "<") {
					assert.Contains(t, *resp.MJML, "&#60;", "« < » écrit en entité numérique dans le titre et l'aperçu")
				}
			})
		}
	}
}
