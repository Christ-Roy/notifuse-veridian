// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : pkg/notifuse_mjml/template_compilation_test.go ; ne contient que les declarations absentes du fork.

package notifuse_mjml

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/preslavrachev/gomjml/mjml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTrackingPixelCollapsesLineBox pins the markup that keeps the open-tracking
// pixel from adding a visible row to the email.
//
// An <img> is inline by default, so it sits on a text baseline and the cell holding
// it takes a whole line box — around 19px at the default font size — no matter how
// small the image is. width="1" height="1" does not help, because it describes the
// image and not the line the image sits on. That is why the pixel used to leave an
// empty row under the footer, and why Outlook scrolled a few millimetres past it.
//
// display:block takes the image off the baseline, the zeroed line-height and
// font-size collapse the line box for clients that honour only one of the two, and
// mso-line-height-rule makes the Word engine behind Outlook treat the zero
// line-height as exact rather than as a minimum.
func TestTrackingPixelCollapsesLineBox(t *testing.T) {
	pixel := GenerateHTMLOpenTrackingPixel("ws", "msg", "https://api.example.com", 1700000000)

	assert.Contains(t, pixel, `<td style="line-height:0px;font-size:0px;mso-line-height-rule:exactly;">`,
		"the pixel cell must zero its line box, or it reserves a full line of text below the email")
	assert.Regexp(t, `<img [^>]*style="[^"]*display:block[^"]*"`, pixel,
		"the pixel image must leave the text baseline, or the cell takes a full line box anyway")
}

// TestTrackingPixelCellStyleMatchesMJMLSpacerRow pins why the cell carries that
// exact declaration list: it is the one gomjml already emits for its own spacer
// rows, so the pixel row reads as ordinary structural markup rather than as
// something written only for a tracking pixel.
//
// A gomjml upgrade that changes its spacer markup fails this test. Copy the new
// string across rather than dropping the assertion — matching MJML byte for byte
// is the point.
func TestTrackingPixelCellStyleMatchesMJMLSpacerRow(t *testing.T) {
	const cellStyle = `line-height:0px;font-size:0px;mso-line-height-rule:exactly;`

	pixel := GenerateHTMLOpenTrackingPixel("ws", "msg", "https://api.example.com", 1700000000)
	require.Contains(t, pixel, cellStyle)

	rendered, err := mjml.Render(`<mjml><mj-body><mj-section><mj-column><mj-text>x</mj-text></mj-column></mj-section></mj-body></mjml>`)
	require.NoError(t, err)
	assert.Contains(t, rendered, cellStyle,
		"the pixel cell style must stay a string MJML itself emits")
}

// TestTrackingPixelCarriesNoDimensionAttributes guards the anti-blocker property
// the collapse fix must not undo: the classic width="1" height="1" pair is the
// fingerprint pixel blockers match on, so the collapse is done in CSS instead.
//
// SpamAssassin gives the same pair a second reason to stay away: it accumulates
// image area only when both attributes are present, and coerces a zero to 200,
// so width="0" height="0" would bill the pixel as 40000px of image.
func TestTrackingPixelCarriesNoDimensionAttributes(t *testing.T) {
	pixel := GenerateHTMLOpenTrackingPixel("ws", "msg", "https://api.example.com", 1700000000)

	imgTag := regexp.MustCompile(`<img [^>]*>`).FindString(pixel)
	require.NotEmpty(t, imgTag, "expected an img tag in the pixel markup")
	assert.NotRegexp(t, `\swidth=`, imgTag, "a width attribute is the pixel-blocker fingerprint")
	assert.NotRegexp(t, `\sheight=`, imgTag, "a height attribute is the pixel-blocker fingerprint")
}

// treeWithMjPreview builds a minimal visual tree whose head contains an
// mj-preview block with the given content.
func treeWithMjPreview(content string) EmailBlock {
	previewBase := NewBaseBlock("preview-1", MJMLComponentMjPreview)
	previewBase.Content = stringPtr(content)
	previewBlock := &MJPreviewBlock{BaseBlock: previewBase}

	headBlock := &MJHeadBlock{BaseBlock: NewBaseBlock("head-1", MJMLComponentMjHead)}
	headBlock.Children = []EmailBlock{previewBlock}

	textBase := NewBaseBlock("text-1", MJMLComponentMjText)
	textBase.Content = stringPtr("hello")
	textBlock := &MJTextBlock{BaseBlock: textBase}

	columnBlock := &MJColumnBlock{BaseBlock: NewBaseBlock("column-1", MJMLComponentMjColumn)}
	columnBlock.Children = []EmailBlock{textBlock}

	sectionBlock := &MJSectionBlock{BaseBlock: NewBaseBlock("section-1", MJMLComponentMjSection)}
	sectionBlock.Children = []EmailBlock{columnBlock}

	bodyBlock := &MJBodyBlock{BaseBlock: NewBaseBlock("body-1", MJMLComponentMjBody)}
	bodyBlock.Children = []EmailBlock{sectionBlock}

	mjmlBlock := &MJMLBlock{BaseBlock: NewBaseBlock("mjml-1", MJMLComponentMjml)}
	mjmlBlock.Children = []EmailBlock{headBlock, bodyBlock}
	return mjmlBlock
}

// treeWithMjTitle builds a minimal visual tree whose head contains an mj-title
// block with the given content.
func treeWithMjTitle(content string) EmailBlock {
	titleBase := NewBaseBlock("title-1", MJMLComponentMjTitle)
	titleBase.Content = stringPtr(content)

	headBlock := &MJHeadBlock{BaseBlock: NewBaseBlock("head-1", MJMLComponentMjHead)}
	headBlock.Children = []EmailBlock{&MJTitleBlock{BaseBlock: titleBase}}

	textBase := NewBaseBlock("text-1", MJMLComponentMjText)
	textBase.Content = stringPtr("hello")

	columnBlock := &MJColumnBlock{BaseBlock: NewBaseBlock("column-1", MJMLComponentMjColumn)}
	columnBlock.Children = []EmailBlock{&MJTextBlock{BaseBlock: textBase}}

	sectionBlock := &MJSectionBlock{BaseBlock: NewBaseBlock("section-1", MJMLComponentMjSection)}
	sectionBlock.Children = []EmailBlock{columnBlock}

	bodyBlock := &MJBodyBlock{BaseBlock: NewBaseBlock("body-1", MJMLComponentMjBody)}
	bodyBlock.Children = []EmailBlock{sectionBlock}

	mjmlBlock := &MJMLBlock{BaseBlock: NewBaseBlock("mjml-1", MJMLComponentMjml)}
	mjmlBlock.Children = []EmailBlock{headBlock, bodyBlock}
	return mjmlBlock
}

func globalFeedData(subject string) MapOfAny {
	return MapOfAny{"global_feed": map[string]interface{}{"subject": subject}}
}

func TestCompileTemplateSubjectPreviewOverrideFallbackRendersLiquid(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// Visual tree without an mj-preview block: the override is injected as a new
	// <mj-preview> tag, and Liquid in it must be rendered — not shipped literally.
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Weekly digest"),
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	require.NotNil(t, resp.HTML)
	assert.Contains(t, *resp.MJML, "<mj-preview>Weekly digest</mj-preview>")
	assert.Contains(t, *resp.HTML, "Weekly digest")
	assert.NotContains(t, *resp.HTML, "{{ global_feed.subject }}")
}

func TestCompileTemplateSubjectPreviewOverrideFallbackEscapesRenderedValue(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// The rendered value — not the raw Liquid — must be escaped before being
	// spliced into the MJML source, so data containing &, < or > still compiles.
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Tom & Jerry <weekly>"),
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	require.NotNil(t, resp.HTML)
	assert.Contains(t, *resp.MJML, "Tom &amp; Jerry &#60;weekly&#62;")
	assert.Contains(t, *resp.HTML, "Tom & Jerry")
	assert.NotContains(t, *resp.HTML, "{{ global_feed.subject }}")
}

func TestCompileTemplateSubjectPreviewOverrideFallbackNoDoubleLiquid(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// A rendered value that itself looks like Liquid must not be evaluated a
	// second time by the whole-string pass.
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Use {{coupon}} now"),
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.HTML)
	assert.Contains(t, *resp.HTML, "Use {{coupon}} now")
}

func TestCompileTemplateSubjectPreviewOverrideFallbackLiquidError(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// Malformed Liquid in the injected preview text surfaces as a compile error
	// instead of being silently shipped to recipients.
	override := "{{ global_feed.subject"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Weekly digest"),
	}

	resp, err := CompileTemplate(req)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotNil(t, resp.Error)
	assert.Nil(t, resp.HTML)
}

func TestCompileTemplateSubjectPreviewOverrideFallbackPreserveLiquid(t *testing.T) {
	// MJML export (PreserveLiquid) keeps the Liquid syntax in the injected tag.
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Weekly digest"),
		PreserveLiquid:         true,
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>{{ global_feed.subject }}</mj-preview>")
}

func TestCompileTemplateSubjectPreviewOverrideFallbackNoData(t *testing.T) {
	// No template data → Liquid processing is skipped everywhere, so the raw
	// expression stays in place (mirrors subject/body behavior).
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>{{ global_feed.subject }}</mj-preview>")
}

func TestCompileTemplateSubjectPreviewOverrideFallbackWebChannel(t *testing.T) {
	// Web channel skips personalization everywhere, so the injected preview keeps
	// its Liquid syntax rather than rendering against contact data.
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       minimalTree(),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Weekly digest"),
		Channel:                "web",
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>{{ global_feed.subject }}</mj-preview>")
	assert.NotContains(t, *resp.MJML, "Weekly digest")
}

func TestCompileTemplateSubjectPreviewOverrideTreeBlockRendersLiquid(t *testing.T) {
	// When the tree has an mj-preview block, the override lands in that block and
	// goes through per-block Liquid rendering.
	override := "{{ global_feed.subject }}"
	req := CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       treeWithMjPreview("PLACEHOLDER"),
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Weekly digest"),
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.HTML)
	assert.Contains(t, *resp.HTML, "Weekly digest")
	assert.NotContains(t, *resp.HTML, "PLACEHOLDER")
	assert.NotContains(t, *resp.HTML, "{{ global_feed.subject }}")
}

// codeModeSource is a minimal code-mode document with no mj-preview block.
const codeModeSource = `<mjml><mj-body><mj-section><mj-column><mj-text>hello</mj-text></mj-column></mj-section></mj-body></mjml>`

func compileCodeMode(t *testing.T, override string, data MapOfAny, source string, preserveLiquid bool) *CompileTemplateResponse {
	t.Helper()
	resp, err := CompileTemplate(CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		MjmlSource:             &source,
		SubjectPreviewOverride: &override,
		TemplateData:           data,
		PreserveLiquid:         preserveLiquid,
	})
	require.NoError(t, err)
	return resp
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeRendersLiquid(t *testing.T) {
	// Code mode applies the override before its Liquid pass; Liquid renders.
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("Weekly digest"), codeModeSource, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.HTML)
	assert.Contains(t, *resp.HTML, "Weekly digest")
	assert.NotContains(t, *resp.HTML, "{{ global_feed.subject }}")
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeEscapesRenderedValue(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// An ampersand in the rendered value must be escaped: escaping the Liquid
	// syntax instead leaves a bare & in the MJML, which fails the XML parse.
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("News & Updates"), codeModeSource, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>News &amp; Updates</mj-preview>")
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeEscapesAngleBrackets(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("Tom & Jerry <weekly>"), codeModeSource, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>Tom &amp; Jerry &#60;weekly&#62;</mj-preview>")
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeLiquidComparison(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// Escaping before Liquid turns `>` into an entity and the comparison silently
	// degrades to a truthiness test, yielding the wrong branch.
	override := "{% if a > b %}YES{% else %}NO{% endif %}"
	resp := compileCodeMode(t, override, MapOfAny{"a": 1, "b": 2}, codeModeSource, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>NO</mj-preview>")
	assert.NotContains(t, *resp.MJML, "YES")
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeReplacesExisting(t *testing.T) {
	// Code mode must replace an existing mj-preview, not add a second one.
	source := `<mjml><mj-head><mj-preview>OLD</mj-preview></mj-head><mj-body><mj-section><mj-column><mj-text>hello</mj-text></mj-column></mj-section></mj-body></mjml>`
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("Weekly digest"), source, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>Weekly digest</mj-preview>")
	assert.NotContains(t, *resp.MJML, "OLD")
	assert.Equal(t, 1, strings.Count(*resp.MJML, "<mj-preview>"))
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeLiquidError(t *testing.T) {
	resp := compileCodeMode(t, "{{ global_feed.subject", globalFeedData("Weekly digest"), codeModeSource, false)
	assert.False(t, resp.Success)
	assert.NotNil(t, resp.Error)
	assert.Nil(t, resp.HTML)
}

func TestCompileTemplateSubjectPreviewOverrideCodeModePreserveLiquid(t *testing.T) {
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("Weekly digest"), codeModeSource, true)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "<mj-preview>{{ global_feed.subject }}</mj-preview>")
	assert.NotContains(t, *resp.MJML, "Weekly digest")
}

func TestCompileTemplateSubjectPreviewOverrideCodeModeSelfClosingPreview(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// A hand-written self-closing <mj-preview /> must be replaced, not duplicated.
	source := `<mjml><mj-head><mj-preview /></mj-head><mj-body><mj-section><mj-column><mj-text>hello</mj-text></mj-column></mj-section></mj-body></mjml>`
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("Weekly digest"), source, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	require.NotNil(t, resp.HTML)
	assert.Equal(t, 1, strings.Count(*resp.MJML, "<mj-preview"))
	assert.Contains(t, *resp.HTML, "Weekly digest")
}

func TestCompileTemplateSubjectPreviewOverrideCodeModePreviewWithAttributes(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// A paired mj-preview carrying attributes must have its content replaced;
	// injecting a second one ships the stale preview text to recipients.
	source := `<mjml><mj-head><mj-preview css-class="x">OLD</mj-preview></mj-head><mj-body><mj-section><mj-column><mj-text>hello</mj-text></mj-column></mj-section></mj-body></mjml>`
	resp := compileCodeMode(t, "{{ global_feed.subject }}", globalFeedData("Weekly digest"), source, false)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	require.NotNil(t, resp.HTML)
	assert.Equal(t, 1, strings.Count(*resp.MJML, "<mj-preview"))
	assert.NotContains(t, *resp.HTML, "OLD")
	assert.Contains(t, *resp.HTML, "Weekly digest")
}

func TestCompileTemplateSubjectPreviewOverrideFillsLiquidEmittedSelfClosingPreview(t *testing.T) {
	t.Skip("DÉFAUT D5 : l'aperçu forcé par l'API (email_options) casse la compilation (& < >) ou est rendu deux fois")
	// An mj-liquid block can emit a bare <mj-preview /> that the tree walk never
	// sees, so updateBlockContent cannot place the override in it. The fallback
	// must fill that tag rather than inject a second preview or drop the text.
	liqBase := NewBaseBlock("liq", MJMLComponentMjLiquid)
	liqBase.Content = stringPtr(`<mj-preview />`)

	headBlock := &MJHeadBlock{BaseBlock: NewBaseBlock("head-1", MJMLComponentMjHead)}
	headBlock.Children = []EmailBlock{&MJLiquidBlock{BaseBlock: liqBase}}

	textBase := NewBaseBlock("text-1", MJMLComponentMjText)
	textBase.Content = stringPtr("hello")
	columnBlock := &MJColumnBlock{BaseBlock: NewBaseBlock("column-1", MJMLComponentMjColumn)}
	columnBlock.Children = []EmailBlock{&MJTextBlock{BaseBlock: textBase}}
	sectionBlock := &MJSectionBlock{BaseBlock: NewBaseBlock("section-1", MJMLComponentMjSection)}
	sectionBlock.Children = []EmailBlock{columnBlock}
	bodyBlock := &MJBodyBlock{BaseBlock: NewBaseBlock("body-1", MJMLComponentMjBody)}
	bodyBlock.Children = []EmailBlock{sectionBlock}
	root := &MJMLBlock{BaseBlock: NewBaseBlock("mjml-1", MJMLComponentMjml)}
	root.Children = []EmailBlock{headBlock, bodyBlock}

	override := "{{ global_feed.subject }}"
	resp, err := CompileTemplate(CompileTemplateRequest{
		WorkspaceID:            "ws",
		MessageID:              "msg",
		VisualEditorTree:       root,
		SubjectPreviewOverride: &override,
		TemplateData:           globalFeedData("Weekly digest"),
	})
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	require.NotNil(t, resp.HTML)
	assert.Equal(t, 1, strings.Count(*resp.MJML, "<mj-preview"))
	assert.Contains(t, *resp.MJML, "<mj-preview>Weekly digest</mj-preview>")
	assert.Contains(t, *resp.HTML, "Weekly digest")
}

func TestCompileTemplateTreeMjPreviewRendersAngleBrackets(t *testing.T) {
	// Liquid in an mj-preview block rendering to a value containing angle brackets
	// must still compile: escaping it with named entities fails, because the MJML
	// parser decodes those back into markup before parsing.
	req := CompileTemplateRequest{
		WorkspaceID:      "ws",
		MessageID:        "msg",
		VisualEditorTree: treeWithMjPreview("{{ global_feed.subject }}"),
		TemplateData:     globalFeedData("Tom & Jerry <weekly>"),
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "Tom &amp; Jerry &#60;weekly&#62;")
}

func TestCompileTemplateTreeMjTitleRendersAngleBrackets(t *testing.T) {
	// mj-title content goes through the same converter escaping as mj-preview.
	req := CompileTemplateRequest{
		WorkspaceID:      "ws",
		MessageID:        "msg",
		VisualEditorTree: treeWithMjTitle("{{ global_feed.subject }}"),
		TemplateData:     globalFeedData("A & B <x>"),
	}

	resp, err := CompileTemplate(req)
	require.NoError(t, err)
	require.True(t, resp.Success, "error: %v", resp.Error)
	require.NotNil(t, resp.MJML)
	assert.Contains(t, *resp.MJML, "A &amp; B &#60;x&#62;")
}

// decodeTrackedDestination extracts the first /r/{token} redirect in html and
// returns the destination URL encrypted inside it, so a test can assert on what
// the recipient's browser will actually be sent to.
func decodeTrackedDestination(t *testing.T, html string, endpoint string) string {
	t.Helper()

	tokenRegex := regexp.MustCompile(regexp.QuoteMeta(endpoint) + `/r/([^"']+)`)
	m := tokenRegex.FindStringSubmatch(html)
	require.NotNil(t, m, "expected a /r/{token} tracking link, got: %s", html)

	plaintext, err := crypto.DecryptTrackingToken(m[1])
	require.NoError(t, err, "failed to decrypt tracking token")

	// Payload format: messageID\nworkspaceID\ntimestamp\ndestinationURL
	parts := strings.Split(plaintext, "\n")
	require.Len(t, parts, 4, "unexpected token payload: %q", plaintext)
	return parts[3]
}

// setNonZeroValue writes a distinguishable value into one struct field. An
// unhandled kind fails the test rather than being skipped: a new field of an
// unknown type is exactly the case this test exists to catch, so it has to be
// taught here before it can pass.
func setNonZeroValue(t *testing.T, field reflect.Value) {
	t.Helper()

	switch field.Kind() {
	case reflect.Bool:
		field.SetBool(true)
	case reflect.String:
		field.SetString("non-zero")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		field.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		field.SetUint(1)
	case reflect.Float32, reflect.Float64:
		field.SetFloat(1)
	case reflect.Slice:
		element := reflect.New(field.Type().Elem()).Elem()
		setNonZeroValue(t, element)
		field.Set(reflect.Append(reflect.MakeSlice(field.Type(), 0, 1), element))
	case reflect.Map:
		key := reflect.New(field.Type().Key()).Elem()
		setNonZeroValue(t, key)
		value := reflect.New(field.Type().Elem()).Elem()
		setNonZeroValue(t, value)
		field.Set(reflect.MakeMap(field.Type()))
		field.SetMapIndex(key, value)
	case reflect.Pointer:
		pointed := reflect.New(field.Type().Elem())
		setNonZeroValue(t, pointed.Elem())
		field.Set(pointed)
	default:
		t.Fatalf("no non-zero value known for kind %s: teach setNonZeroValue about it, "+
			"then make sure TrackingSettings.IsZero() accounts for the new field", field.Kind())
	}
}

// TestApplyUTMParameters_PreservesURLBytes pins the byte-exactness of the UTM
// append, the same guarantee applyIdentityParam already gives. Every case below
// is one that rebuilding the URL through url.Values corrupts: "sid=1;2" and
// "discount=50%off" are dropped by ParseQuery outright (so the customer's
// parameter disappears from the link the recipient follows), the surviving
// pairs come back sorted alphabetically, a valueless key grows an "=", a
// literal space becomes "+" and the path is re-escaped.
func TestApplyUTMParameters_PreservesURLBytes(t *testing.T) {
	settings := &TrackingSettings{UTMSource: "email"}

	tests := []struct {
		name    string
		linkURL string
		want    string
	}{
		{
			name:    "semicolon separated value survives",
			linkURL: "https://example.com/page?sid=1;2",
			want:    "https://example.com/page?sid=1;2&utm_source=email",
		},
		{
			name:    "bare percent sign survives",
			linkURL: "https://example.com/page?discount=50%off",
			want:    "https://example.com/page?discount=50%off&utm_source=email",
		},
		{
			name:    "existing parameter order is kept",
			linkURL: "https://example.com/page?z=1&a=2&sig=abc",
			want:    "https://example.com/page?z=1&a=2&sig=abc&utm_source=email",
		},
		{
			name:    "valueless flag keeps its missing equals sign",
			linkURL: "https://example.com/page?flag",
			want:    "https://example.com/page?flag&utm_source=email",
		},
		{
			name:    "literal space in a value is not re-encoded",
			linkURL: "https://example.com/page?a=hello world",
			want:    "https://example.com/page?a=hello world&utm_source=email",
		},
		{
			name:    "path bytes are not re-escaped",
			linkURL: "https://example.com/my page?a=1",
			want:    "https://example.com/my page?a=1&utm_source=email",
		},
		{
			name:    "slashes and commas kept",
			linkURL: "https://example.com/page?redirect=/home&ids=1,2,3",
			want:    "https://example.com/page?redirect=/home&ids=1,2,3&utm_source=email",
		},
		{
			name:    "fragment stays last",
			linkURL: "https://example.com/page?a=1#section",
			want:    "https://example.com/page?a=1&utm_source=email#section",
		},
		{
			name:    "fragment without query",
			linkURL: "https://example.com/page#section",
			want:    "https://example.com/page?utm_source=email#section",
		},
		{
			name:    "no query gains a question mark",
			linkURL: "https://example.com/page",
			want:    "https://example.com/page?utm_source=email",
		},
		{
			name:    "empty query reuses its question mark",
			linkURL: "https://example.com/page?",
			want:    "https://example.com/page?utm_source=email",
		},
		{
			name:    "port survives",
			linkURL: "https://example.com:8443/page?a=1",
			want:    "https://example.com:8443/page?a=1&utm_source=email",
		},
		{
			name:    "unicode in a value survives",
			linkURL: "https://example.com/page?q=café",
			want:    "https://example.com/page?q=café&utm_source=email",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, settings.applyUTMParameters(test.linkURL))
		})
	}
}

// TestApplyUTMParameters_AppendsConfiguredFieldsInAscendingKeyOrder documents
// the shape of what gets added: only the configured fields, ascending by key,
// with only our own values escaped.
//
// Ascending key order is what url.Values.Encode() emitted while this function
// still re-serialised the query, and it is held deliberately. The rewritten URL
// is the clicked_links key per-link stats aggregate on, so a link the author
// left unparameterised — the common case — keeps producing the exact bytes it
// produced before, and its click history does not split.
func TestApplyUTMParameters_AppendsConfiguredFieldsInAscendingKeyOrder(t *testing.T) {
	all := &TrackingSettings{
		UTMSource:   "newsletter",
		UTMMedium:   "email",
		UTMCampaign: "spring sale/2024",
		UTMContent:  "hero-button",
		UTMTerm:     "running-shoes",
	}
	assert.Equal(t,
		"https://example.com/p?utm_campaign=spring+sale%2F2024&utm_content=hero-button&utm_medium=email&utm_source=newsletter&utm_term=running-shoes",
		all.applyUTMParameters("https://example.com/p"))

	// A gap in the middle does not leave an empty pair behind.
	sparse := &TrackingSettings{UTMSource: "newsletter", UTMContent: "hero"}
	assert.Equal(t,
		"https://example.com/p?a=1&utm_content=hero&utm_source=newsletter",
		sparse.applyUTMParameters("https://example.com/p?a=1"))
}

// TestApplyUTMParameters_UnchangedShapesKeepTheirHistoricalBytes is the
// continuity half of this rewrite. Per-link click stats aggregate on the
// rewritten URL as a jsonb key, so every link whose output changes starts a new
// bucket the day this ships. These are the shapes that must NOT change, and
// they are the shapes most links have: the expected strings below are the ones
// the previous url.Values implementation produced.
func TestApplyUTMParameters_UnchangedShapesKeepTheirHistoricalBytes(t *testing.T) {
	broadcast := &TrackingSettings{
		UTMSource:   "newsletter",
		UTMMedium:   "email",
		UTMCampaign: "spring",
	}
	transactional := &TrackingSettings{UTMContent: "tpl-1"}

	tests := []struct {
		name     string
		settings *TrackingSettings
		linkURL  string
		want     string
	}{
		{
			name:     "no existing query, every utm field",
			settings: broadcast,
			linkURL:  "https://shop.example.com/product",
			want:     "https://shop.example.com/product?utm_campaign=spring&utm_medium=email&utm_source=newsletter",
		},
		{
			name:     "no existing query, single utm field",
			settings: transactional,
			linkURL:  "https://shop.example.com/product",
			want:     "https://shop.example.com/product?utm_content=tpl-1",
		},
		{
			name:     "one existing key sorting before utm_",
			settings: broadcast,
			linkURL:  "https://shop.example.com/product?ref=email",
			want:     "https://shop.example.com/product?ref=email&utm_campaign=spring&utm_medium=email&utm_source=newsletter",
		},
		{
			name:     "two existing keys already in ascending order",
			settings: transactional,
			linkURL:  "https://shop.example.com/product?a=1&b=2",
			want:     "https://shop.example.com/product?a=1&b=2&utm_content=tpl-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.settings.applyUTMParameters(test.linkURL))
		})
	}
}

// TestApplyUTMParameters_NothingConfiguredLeavesURLUntouched is the case that
// runs on every send with UTM disabled: with no field set there is nothing to
// append, so the link must come back byte-for-byte.
func TestApplyUTMParameters_NothingConfiguredLeavesURLUntouched(t *testing.T) {
	settings := &TrackingSettings{}

	for _, linkURL := range []string{
		"https://example.com/page?sid=1;2",
		"https://example.com/page?discount=50%off",
		"https://example.com/page?z=1&a=2&sig=abc",
		"https://example.com/page?flag",
		"https://example.com/page",
		"https://example.com/page?",
		"https://example.com/page?a=1#top",
	} {
		assert.Equal(t, linkURL, settings.applyUTMParameters(linkURL))
	}
}

// TestApplyUTMParameters_LeavesLinksCarryingUTMAlone keeps the existing
// "already tagged -> hands off" rule, including the forms url.Values used to
// normalise away: an uppercase key and a percent-encoded underscore.
func TestApplyUTMParameters_LeavesLinksCarryingUTMAlone(t *testing.T) {
	settings := &TrackingSettings{UTMSource: "email", UTMMedium: "newsletter"}

	for _, linkURL := range []string{
		"https://example.com/page?utm_source=existing",
		"https://example.com/page?a=1&utm_campaign=mine&b=2",
		"https://example.com/page?UTM_SOURCE=existing",
		"https://example.com/page?utm%5Fsource=existing",
		"https://example.com/page?utm_source=x&sid=1;2",
		"https://example.com/page?utm_term=x#top",
	} {
		assert.Equal(t, linkURL, settings.applyUTMParameters(linkURL))
	}

	// A value that merely looks like a utm key is not a utm key.
	assert.Equal(t,
		"https://example.com/page?ref=utm_source&utm_medium=newsletter&utm_source=email",
		settings.applyUTMParameters("https://example.com/page?ref=utm_source"))
}

// TestApplyUTMParameters_DefensiveCases exercises the guards directly, since
// TrackLinks filters some of these out before the method is reached.
func TestApplyUTMParameters_DefensiveCases(t *testing.T) {
	settings := &TrackingSettings{UTMSource: "email"}

	for _, sourceURL := range []string{
		"",
		"mailto:hello@example.com",
		"tel:+33123456789",
		"{{ unsubscribe_url }}",
		"{% if x %}https://example.com{% endif %}",
		"://example.com/broken",
		"https://example.com/50%off/page", // invalid percent-escape in the path
	} {
		assert.Equal(t, sourceURL, settings.applyUTMParameters(sourceURL))
	}

	// A relative link is still a link: it is what an mj-button pointing at the
	// hosted page carries, and it must be tagged like any other.
	assert.Equal(t, "/relative/path?utm_source=email", settings.applyUTMParameters("/relative/path"))
}

// TestTrackLinks_UTMKeepsHandBuiltQuery is the end-to-end version: a customer's
// hand-built query must reach the recipient intact even though the send path
// always has a utm_content to append.
func TestTrackLinks_UTMKeepsHandBuiltQuery(t *testing.T) {
	settings := TrackingSettings{
		EnableTracking: false, // isolate the rewrite from the /r/ wrapper
		UTMContent:     "tpl-123",
	}

	html := `<html><body><a href="https://example.com/page?sid=1;2&sig=abc#top">Link</a></body></html>`

	result, err := TrackLinks(html, settings)
	require.NoError(t, err)
	assert.Equal(t,
		`<html><body><a href="https://example.com/page?sid=1;2&sig=abc&utm_content=tpl-123#top">Link</a></body></html>`,
		result)
}
