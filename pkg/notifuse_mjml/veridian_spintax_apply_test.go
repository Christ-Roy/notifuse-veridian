package notifuse_mjml

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianApplySpintax_EmptySeedIsNoOp(t *testing.T) {
	// Seed vide = no-op strict, MÊME si l'entrée contient du spintax.
	// C'est le garde-fou de non-régression upstream.
	in := "{Bonjour|Salut} le monde"
	assert.Equal(t, in, veridianApplySpintax(in, ""), "seed vide ne doit rien résoudre")
}

func TestVeridianApplySpintax_ResolvesWithSeed(t *testing.T) {
	in := "{Bonjour|Salut} le monde"
	out := veridianApplySpintax(in, "x@y.com")
	assert.True(t,
		out == "Bonjour le monde" || out == "Salut le monde",
		"une variante doit être choisie, eu %q", out)
	// Déterminisme.
	assert.Equal(t, out, veridianApplySpintax(in, "x@y.com"))
}

func TestVeridianApplySpintax_NoSpintaxIsIdentity(t *testing.T) {
	in := "<p>Email parfaitement normal</p>"
	assert.Equal(t, in, veridianApplySpintax(in, "seed@x.com"))
}

// TestCompileTemplate_SpintaxIntegration vérifie le câblage de bout en bout dans
// le pipeline : résolution du corps en mode MjmlSource, préservation Liquid, et
// no-op strict sans seed.
func TestCompileTemplate_SpintaxResolvedInBody(t *testing.T) {
	mjml := "<mjml><mj-body><mj-section><mj-column><mj-text>" +
		"{Bonjour|Salut} cher client" +
		"</mj-text></mj-column></mj-section></mj-body></mjml>"

	req := CompileTemplateRequest{
		WorkspaceID:         "ws1",
		MessageID:           "msg1",
		MjmlSource:          &mjml,
		VeridianSpintaxSeed: "prospect@example.com",
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "compilation doit réussir: %+v", resp.Error)
	require.NotNil(t, resp.HTML)

	html := *resp.HTML
	// Exactement une des deux variantes, jamais le spintax brut.
	hasBonjour := strings.Contains(html, "Bonjour cher client")
	hasSalut := strings.Contains(html, "Salut cher client")
	assert.True(t, hasBonjour != hasSalut, "exactement une variante attendue, eu bonjour=%v salut=%v", hasBonjour, hasSalut)
	assert.NotContains(t, html, "{Bonjour|Salut}", "le spintax brut ne doit pas subsister")
	assert.NotContains(t, html, "|cher", "pas de pipe résiduel")
}

func TestCompileTemplate_SpintaxNoOpWithoutSeed(t *testing.T) {
	// Sans seed, un template SANS spintax compile exactement comme upstream
	// (non-régression). On compile deux fois — avec et sans le champ — et on
	// vérifie l'égalité du HTML.
	mjml := "<mjml><mj-body><mj-section><mj-column><mj-text>" +
		"Bonjour cher client" +
		"</mj-text></mj-column></mj-section></mj-body></mjml>"

	reqNoSeed := CompileTemplateRequest{WorkspaceID: "ws1", MessageID: "msg1", MjmlSource: &mjml}
	reqWithSeed := CompileTemplateRequest{WorkspaceID: "ws1", MessageID: "msg1", MjmlSource: &mjml, VeridianSpintaxSeed: "anyone@x.com"}

	respNoSeed, err := CompileTemplate(reqNoSeed)
	require.NoError(t, err)
	require.True(t, respNoSeed.Success)

	respWithSeed, err := CompileTemplate(reqWithSeed)
	require.NoError(t, err)
	require.True(t, respWithSeed.Success)

	// Un template sans spintax doit produire le même HTML, avec ou sans seed.
	require.NotNil(t, respNoSeed.HTML)
	require.NotNil(t, respWithSeed.HTML)
	assert.Equal(t, *respNoSeed.HTML, *respWithSeed.HTML,
		"pas de spintax → seed ne doit rien changer (no-op strict)")
}

func TestCompileTemplate_SpintaxPreservesLiquidInBody(t *testing.T) {
	// Variable Liquid hors et dans le spintax. TemplateData fournit la valeur.
	mjml := "<mjml><mj-body><mj-section><mj-column><mj-text>" +
		"{Bonjour|Salut} {{ contact.first_name }}" +
		"</mj-text></mj-column></mj-section></mj-body></mjml>"

	req := CompileTemplateRequest{
		WorkspaceID:         "ws1",
		MessageID:           "msg1",
		MjmlSource:          &mjml,
		TemplateData:        MapOfAny{"contact": map[string]any{"first_name": "Jean"}},
		VeridianSpintaxSeed: "jean@example.com",
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "compilation doit réussir: %+v", resp.Error)
	require.NotNil(t, resp.HTML)

	html := *resp.HTML
	// Liquid rendu (la variable a été remplacée par "Jean") ET spintax résolu.
	assert.Contains(t, html, "Jean", "la variable Liquid doit être rendue")
	assert.NotContains(t, html, "{{", "aucune accolade Liquid résiduelle")
	assert.True(t,
		strings.Contains(html, "Bonjour Jean") || strings.Contains(html, "Salut Jean"),
		"corps spintax+liquid résolu attendu, eu: %q", html)
}

func TestCompileTemplate_SpintaxResolvedInSubject(t *testing.T) {
	subject := "{Offre|Promo} exclusive"
	mjml := "<mjml><mj-body><mj-section><mj-column><mj-text>corps</mj-text></mj-column></mj-section></mj-body></mjml>"

	req := CompileTemplateRequest{
		WorkspaceID:         "ws1",
		MessageID:           "msg1",
		MjmlSource:          &mjml,
		Subject:             &subject,
		TemplateData:        MapOfAny{"x": "y"}, // non vide pour que renderSubjectField traite le champ
		VeridianSpintaxSeed: "subj@example.com",
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "compilation doit réussir: %+v", resp.Error)
	require.NotNil(t, resp.Subject)

	out := *resp.Subject
	assert.True(t,
		out == "Offre exclusive" || out == "Promo exclusive",
		"sujet spintax résolu attendu, eu %q", out)
	assert.NotContains(t, out, "|")
}

func TestCompileTemplate_SubjectSpintaxNoOpWithoutSeed(t *testing.T) {
	subject := "{Offre|Promo} exclusive"
	mjml := "<mjml><mj-body><mj-section><mj-column><mj-text>corps</mj-text></mj-column></mj-section></mj-body></mjml>"

	req := CompileTemplateRequest{
		WorkspaceID:  "ws1",
		MessageID:    "msg1",
		MjmlSource:   &mjml,
		Subject:      &subject,
		TemplateData: MapOfAny{"x": "y"},
		// Pas de seed.
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success)
	require.NotNil(t, resp.Subject)
	// Sans seed, le sujet reste avec son spintax brut (no-op).
	assert.Equal(t, "{Offre|Promo} exclusive", *resp.Subject)
}
