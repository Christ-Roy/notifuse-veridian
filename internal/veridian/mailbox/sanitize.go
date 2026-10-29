package mailbox

import (
	"encoding/base64"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	policyOnce sync.Once
	policy     *bluemonday.Policy
)

// safeStyleProps : propriétés CSS en ligne conservées (mise en forme, jamais de positionnement
// hors flux ni de chargement de ressource).
var safeStyleProps = []string{
	"color", "background-color", "font", "font-family", "font-size", "font-style", "font-weight",
	"line-height", "letter-spacing", "text-align", "text-decoration", "text-transform", "text-indent",
	"vertical-align", "white-space", "word-break", "word-wrap", "overflow-wrap",
	"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
	"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
	"border", "border-top", "border-right", "border-bottom", "border-left", "border-color", "border-style",
	"border-width", "border-radius", "border-collapse", "border-spacing",
	"width", "height", "max-width", "min-width", "max-height", "min-height",
	"display", "float", "clear", "list-style", "list-style-type", "table-layout", "opacity",
}

func styleValueSafe(v string) bool {
	l := strings.ToLower(v)
	for _, bad := range []string{"url(", "expression", "javascript", "@import", "\\", "behavior", "binding", "<", ">", "image-set", "var("} {
		if strings.Contains(l, bad) {
			return false
		}
	}
	return len(v) <= 300
}

func buildPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("a", "abbr", "address", "article", "aside", "b", "big", "blockquote", "br", "caption", "center",
		"cite", "code", "col", "colgroup", "dd", "del", "div", "dl", "dt", "em", "figcaption", "figure", "font",
		"footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i", "img", "ins", "kbd", "li", "mark", "nav",
		"ol", "p", "pre", "q", "s", "section", "small", "span", "strike", "strong", "sub", "sup", "table", "tbody",
		"td", "tfoot", "th", "thead", "tr", "tt", "u", "ul")
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("src", "alt", "width", "height", "align", "border").OnElements("img")
	p.AllowAttrs("title").Globally()
	p.AllowAttrs("dir").Matching(regexp.MustCompile(`^(?i)(ltr|rtl|auto)$`)).Globally()
	p.AllowAttrs("lang").Matching(regexp.MustCompile(`^[a-zA-Z-]{2,12}$`)).Globally()
	p.AllowAttrs("align", "valign", "bgcolor", "width", "height", "border", "cellpadding", "cellspacing", "colspan", "rowspan", "nowrap").
		OnElements("table", "tr", "td", "th", "tbody", "thead", "tfoot", "col", "colgroup", "div", "p", "center", "hr")
	p.AllowAttrs("color", "face", "size").OnElements("font")
	p.AllowAttrs("style").Globally()
	p.AllowStyles(safeStyleProps...).MatchingHandler(styleValueSafe).Globally()
	p.AllowURLSchemes("http", "https", "mailto", "tel")
	p.AllowURLSchemeWithCustomPolicy("cid", func(*url.URL) bool { return true })
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.AllowRelativeURLs(false)
	p.SkipElementsContent("script", "style", "head", "title", "iframe", "object", "embed", "noscript", "template", "svg", "math", "form", "textarea", "select", "button", "applet", "frameset", "frame", "base", "meta", "link")
	return p
}

var cidSrcRe = regexp.MustCompile(`src="cid:([^"]+)"`)

// SanitizeHTML assainit le HTML d'un mail reçu : plus de script, de gestionnaire d'événement,
// de formulaire, de cadre, de <style> ni de ressource CSS ; liens limités à http, https,
// mailto, tel ; images distantes conservées (le rendu les bloque par défaut dans l'iframe).
// Les images cid: connues sont remplacées par une URI data: (type image validé).
func SanitizeHTML(raw string, cids map[string]InlineImage) string {
	policyOnce.Do(func() { policy = buildPolicy() })
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	out := policy.Sanitize(raw)
	out = cidSrcRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := cidSrcRe.FindStringSubmatch(m)
		id := strings.ToLower(html.UnescapeString(sub[1]))
		img, ok := cids[id]
		if !ok || !safeImageType(img.ContentType) {
			return `src="" alt="image incorporée absente"`
		}
		return `src="data:` + img.ContentType + `;base64,` + base64.StdEncoding.EncodeToString(img.Data) + `"`
	})
	return out
}

func safeImageType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp":
		return true
	}
	return false
}

var (
	linkRe   = regexp.MustCompile(`https?://[^\s<>"')]+`)
	htmlTags = regexp.MustCompile(`(?s)<[^>]*>`)
)

// TextToHTML met un texte brut en HTML sûr (échappé, liens cliquables, paragraphes).
func TextToHTML(text string) string {
	esc := html.EscapeString(text)
	esc = linkRe.ReplaceAllStringFunc(esc, func(u string) string {
		return `<a href="` + u + `" target="_blank" rel="noopener nofollow noreferrer">` + u + `</a>`
	})
	esc = strings.ReplaceAll(esc, "\r\n", "\n")
	return `<div style="white-space:pre-wrap">` + esc + `</div>`
}

// HTMLToText donne un texte lisible depuis du HTML (corps texte absent).
func HTMLToText(h string) string {
	if h == "" {
		return ""
	}
	r := strings.NewReplacer("</p>", "\n\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n", "</div>", "\n", "</tr>", "\n", "</li>", "\n", "</h1>", "\n\n", "</h2>", "\n\n", "</h3>", "\n\n")
	t := r.Replace(h)
	t = htmlTags.ReplaceAllString(t, "")
	t = html.UnescapeString(t)
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	t = strings.Join(lines, "\n")
	t = regexp.MustCompile(`\n{3,}`).ReplaceAllString(t, "\n\n")
	return strings.TrimSpace(t)
}
