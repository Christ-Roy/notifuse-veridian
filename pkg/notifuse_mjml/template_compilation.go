package notifuse_mjml

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/preslavrachev/gomjml/mjml"
)

// htmlVoidElements are HTML elements that must be self-closing in XML
var htmlVoidElements = []string{
	"area", "base", "br", "col", "embed", "hr", "img", "input",
	"link", "meta", "param", "source", "track", "wbr",
}

// htmlEntityToCodepoint maps HTML named entities to their Unicode code points
// Only entities not predefined in XML (amp, lt, gt, quot, apos) need conversion
var htmlEntityToCodepoint = map[string]int{
	// Whitespace and formatting
	"nbsp": 160, "ensp": 8194, "emsp": 8195, "thinsp": 8201,
	// Punctuation
	"bull": 8226, "hellip": 8230, "mdash": 8212, "ndash": 8211,
	"lsquo": 8216, "rsquo": 8217, "ldquo": 8220, "rdquo": 8221,
	"laquo": 171, "raquo": 187,
	// Symbols
	"copy": 169, "reg": 174, "trade": 8482, "sect": 167, "para": 182,
	"deg": 176, "plusmn": 177, "times": 215, "divide": 247,
	"micro": 181, "middot": 183,
	// Currency
	"euro": 8364, "pound": 163, "yen": 165, "cent": 162,
	// Arrows
	"larr": 8592, "rarr": 8594, "uarr": 8593, "darr": 8595, "harr": 8596,
	// Spanish/French punctuation
	"iexcl": 161, "iquest": 191,
}

// preprocessMjmlForXML preprocesses MJML string to fix common HTML vs XML incompatibilities
// This is necessary because gomjml uses a strict XML parser
func preprocessMjmlForXML(mjmlString string) string {
	processed := mjmlString

	// Step 1: Convert HTML void tags to self-closing XML format
	// HTML allows <br>, <hr>, <img>, etc. without closing slash
	// XML requires self-closing: <br/>, <hr/>, <img/>
	// Match: <br>, <br >, <hr>, <img src="...">, etc.
	// Don't match: <br/>, <br />
	voidTagPattern := regexp.MustCompile(
		`(?i)<(` + strings.Join(htmlVoidElements, "|") + `)(\s[^>]*)?>`,
	)
	processed = voidTagPattern.ReplaceAllStringFunc(processed, func(match string) string {
		// Check if already self-closing (ends with /> or / >)
		trimmed := strings.TrimSpace(match)
		if strings.HasSuffix(trimmed, "/>") {
			return match
		}

		// Extract tag name and attributes using submatch
		parts := voidTagPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		tagName := parts[1]
		attrs := ""
		if len(parts) > 2 && parts[2] != "" {
			attrs = strings.TrimRight(parts[2], " ")
		}
		return "<" + tagName + attrs + "/>"
	})

	// Step 2: Convert HTML named entities to XML numeric entities
	// XML only predefines: &amp; &lt; &gt; &quot; &apos;
	// HTML entities like &nbsp; must be converted to &#160;
	entityPattern := regexp.MustCompile(`&([a-zA-Z]+);`)
	processed = entityPattern.ReplaceAllStringFunc(processed, func(match string) string {
		// Extract entity name (without & and ;)
		entityName := strings.ToLower(match[1 : len(match)-1])

		// Preserve XML predefined entities
		if entityName == "amp" || entityName == "lt" || entityName == "gt" ||
			entityName == "quot" || entityName == "apos" {
			return match
		}

		// Convert known HTML entities to numeric
		if codepoint, ok := htmlEntityToCodepoint[entityName]; ok {
			return fmt.Sprintf("&#%d;", codepoint)
		}

		// Unknown entity - leave as-is
		return match
	})

	return processed
}

// MapOfAny represents a map of string to any value, used for template data
type MapOfAny map[string]any

type TrackingSettings struct {
	EnableTracking bool `json:"enable_tracking"`

	// Veridian fork — EnableOpenPixel découple le pixel d'OUVERTURE (email.opened)
	// de la réécriture de liens (EnableTracking, clics). Le tunnel cold veut le
	// pixel ON sur les petits providers (freemail_fr, yahoo_aol, corporate) mais
	// OFF sur google/microsoft (réputation sensible), tout en gardant le tracking
	// de clics partout. Pointeur nullable pour NON-RÉGRESSION STRICTE : nil =
	// comportement upstream (le pixel suit EnableTracking) ; non-nil = override
	// veridian explicite (true force le pixel, false le supprime) indépendamment
	// d'EnableTracking. Posé par VeridianResolveOpenPixel (queue_message_sender).
	EnableOpenPixel *bool `json:"enable_open_pixel,omitempty"`

	Endpoint    string `json:"endpoint,omitempty"`
	UTMSource   string `json:"utm_source,omitempty"`
	UTMMedium   string `json:"utm_medium,omitempty"`
	UTMCampaign string `json:"utm_campaign,omitempty"`
	UTMContent  string `json:"utm_content,omitempty"`
	UTMTerm     string `json:"utm_term,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
}

// Value implements the driver.Valuer interface for database storage
func (t TrackingSettings) Value() (driver.Value, error) {
	return json.Marshal(t)
}

// Scan implements the sql.Scanner interface for database retrieval
func (t *TrackingSettings) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	v, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("type assertion to []byte failed for TrackingSettings")
	}

	return json.Unmarshal(v, t)
}

// isNonTrackableURL checks if a URL should not have click tracking applied.
// This includes special protocol links (mailto, tel, sms, etc.), template placeholders,
// and anchor links that should not be redirected through the tracking endpoint.
func isNonTrackableURL(urlStr string) bool {
	if urlStr == "" {
		return true
	}

	// Skip template placeholders (Liquid syntax)
	if strings.Contains(urlStr, "{{") || strings.Contains(urlStr, "{%") {
		return true
	}

	// Skip anchor-only links
	if strings.HasPrefix(urlStr, "#") {
		return true
	}

	// Skip special protocol links that should not be tracked
	lowerURL := strings.ToLower(urlStr)
	nonTrackableProtocols := []string{
		"mailto:",
		"tel:",
		"sms:",
		"javascript:",
		"data:",
		"blob:",
		"file:",
	}

	for _, protocol := range nonTrackableProtocols {
		if strings.HasPrefix(lowerURL, protocol) {
			return true
		}
	}

	return false
}

// splitURLFragment coupe sourceURL au premier '#'. Le fragment garde son '#' : head+fragment redonne
// toujours la chaîne d'origine. Un paramètre appartient à la requête, jamais après '#'.
// Port de Notifuse v39.1 (AGPL-3.0), voir docs/claude/75.
func splitURLFragment(sourceURL string) (head string, fragment string) {
	if hash := strings.IndexByte(sourceURL, '#'); hash >= 0 {
		return sourceURL[:hash], sourceURL[hash:]
	}
	return sourceURL, ""
}

// rawQueryHasKey indique si la requête brute porte une paire dont la clé satisfait match.
// La requête est lue telle qu'écrite, jamais décodée.
func rawQueryHasKey(query string, match func(key string) bool) bool {
	if query == "" {
		return false
	}
	for _, pair := range strings.Split(query, "&") {
		key := pair
		if eq := strings.IndexByte(pair, '='); eq >= 0 {
			key = pair[:eq]
		}
		if match(key) {
			return true
		}
	}
	return false
}

// rawQueryOf rend ce qui est entre le premier '?' et le fragment, et si l'URL porte un '?'.
func rawQueryOf(sourceURL string) (query string, hasQuery bool) {
	head, _ := splitURLFragment(sourceURL)
	mark := strings.IndexByte(head, '?')
	if mark < 0 {
		return "", false
	}
	return head[mark+1:], true
}

// appendToRawQuery ajoute addition (déjà échappée) à la fin de la requête par chirurgie sur la chaîne,
// pour que chaque autre octet du lien survive tel que son auteur l'a écrit. Reconstruire l'URL par
// url.Values perdait des paires ("sid=1;2", "discount=50%off"), réordonnait les autres (signature
// dépendante de l'ordre) et réencodait le chemin.
func appendToRawQuery(sourceURL string, addition string) string {
	head, fragment := splitURLFragment(sourceURL)

	mark := strings.IndexByte(head, '?')
	switch {
	case mark < 0:
		return head + "?" + addition + fragment
	case mark == len(head)-1:
		return head + addition + fragment
	default:
		return head + "&" + addition + fragment
	}
}

// isUTMKey indique si une clé brute est un paramètre UTM ("utm%5Fsource" == "utm_source").
func isUTMKey(key string) bool {
	if decoded, err := url.QueryUnescape(key); err == nil {
		key = decoded
	}
	return strings.HasPrefix(strings.ToLower(key), "utm_")
}

// applyUTMParameters ajoute les paramètres UTM configurés à sourceURL. L'URL est rendue inchangée si
// elle est vide, un marqueur Liquid, mailto:/tel:, illisible, porte déjà un paramètre utm_*, ou si
// aucun champ UTM n'est configuré. Port de Notifuse v39.1 (AGPL-3.0).
func (t *TrackingSettings) applyUTMParameters(sourceURL string) string {
	if sourceURL == "" || strings.Contains(sourceURL, "{{") || strings.Contains(sourceURL, "{%") ||
		strings.HasPrefix(sourceURL, "mailto:") || strings.HasPrefix(sourceURL, "tel:") {
		return sourceURL
	}

	// url.Parse ne sert qu'à refuser ce qui n'est pas une URL ; la chaîne qu'il reconstruirait n'est jamais utilisée.
	if _, err := url.Parse(sourceURL); err != nil {
		return sourceURL
	}

	if query, hasQuery := rawQueryOf(sourceURL); hasQuery && rawQueryHasKey(query, isUTMKey) {
		return sourceURL
	}

	// Champs configurés seulement, en ordre de clé croissant : c'est l'ordre qu'url.Values.Encode()
	// émettait avant ce correctif, et l'URL réécrite sert de clé aux statistiques de clic par lien
	// (jsonb clicked_links) : changer ces octets scinderait l'historique d'un lien en deux.
	var pairs []string
	for _, param := range []struct{ key, value string }{
		{"utm_campaign", t.UTMCampaign},
		{"utm_content", t.UTMContent},
		{"utm_medium", t.UTMMedium},
		{"utm_source", t.UTMSource},
		{"utm_term", t.UTMTerm},
	} {
		if param.value != "" {
			pairs = append(pairs, param.key+"="+url.QueryEscape(param.value))
		}
	}
	if len(pairs) == 0 {
		return sourceURL
	}

	return appendToRawQuery(sourceURL, strings.Join(pairs, "&"))
}

func (t *TrackingSettings) GetTrackingURL(sourceURL string) string {
	// Ignore if URL is empty, a placeholder, mailto:, tel:, or already tracked (basic check)
	if sourceURL == "" || strings.Contains(sourceURL, "{{") || strings.Contains(sourceURL, "{%") || strings.HasPrefix(sourceURL, "mailto:") || strings.HasPrefix(sourceURL, "tel:") {
		return sourceURL
	}

	// Append UTM parameters to the destination URL
	destinationURL := t.applyUTMParameters(sourceURL)

	if !t.EnableTracking {
		return destinationURL
	}

	// parse endpoint and add the UTM-augmented destination URL to the query params
	parsedEndpoint, err := url.Parse(t.Endpoint)
	if err != nil {
		return sourceURL
	}
	endpointParams := parsedEndpoint.Query()
	endpointParams.Add("url", destinationURL)
	parsedEndpoint.RawQuery = endpointParams.Encode()

	return parsedEndpoint.String()
}

// CompileTemplateRequest represents the request for compiling a template
type CompileTemplateRequest struct {
	WorkspaceID            string           `json:"workspace_id"`
	MessageID              string           `json:"message_id"`
	VisualEditorTree       EmailBlock       `json:"visual_editor_tree"`
	MjmlSource             *string          `json:"mjml_source,omitempty"`
	Subject                *string          `json:"subject,omitempty"`         // Email subject; processed through Liquid using TemplateData
	SubjectPreview         *string          `json:"subject_preview,omitempty"` // Email subject preview (inbox preview text); processed through Liquid
	TemplateData           MapOfAny         `json:"test_data,omitempty"`
	TrackingSettings       TrackingSettings `json:"tracking_settings,omitempty"`
	Channel                string           `json:"channel,omitempty"`                  // "email" or "web"
	PreserveLiquid         bool             `json:"preserve_liquid,omitempty"`          // When true, skip Liquid template processing and preserve raw syntax
	SubjectPreviewOverride *string          `json:"subject_preview_override,omitempty"` // Override mj-preview content before compilation

	// VeridianSpintaxSeed (fork Veridian, cold outbound) : graine déterministe de
	// résolution spintax `{a|b|c}`, typiquement l'email du destinataire. Posée
	// par les senders broadcast. Vide = AUCUNE résolution spintax (no-op strict,
	// non-régression upstream totale). Voir pkg/veridian_spintax.
	VeridianSpintaxSeed string `json:"veridian_spintax_seed,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling for CompileTemplateRequest
func (r *CompileTemplateRequest) UnmarshalJSON(data []byte) error {
	// Create a temporary struct with the same fields but using json.RawMessage for VisualEditorTree
	type Alias CompileTemplateRequest
	aux := &struct {
		*Alias
		VisualEditorTree json.RawMessage `json:"visual_editor_tree"`
	}{
		Alias: (*Alias)(r),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	// Unmarshal the VisualEditorTree using our custom function
	if len(aux.VisualEditorTree) > 0 {
		block, err := UnmarshalEmailBlock(aux.VisualEditorTree)
		if err != nil {
			// If MjmlSource is provided, we can skip visual_editor_tree parsing errors
			if r.MjmlSource != nil && *r.MjmlSource != "" {
				return nil
			}
			return fmt.Errorf("failed to unmarshal visual_editor_tree: %w", err)
		}
		r.VisualEditorTree = block
	}

	return nil
}

// Validate ensures that the compile template request has all required fields
func (r *CompileTemplateRequest) Validate() error {
	if r.WorkspaceID == "" {
		return fmt.Errorf("invalid compile template request: workspace_id is required")
	}
	if r.MessageID == "" {
		return fmt.Errorf("invalid compile template request: message_id is required")
	}

	// Accept either MjmlSource or VisualEditorTree
	if r.MjmlSource != nil && *r.MjmlSource != "" {
		// MjmlSource is provided, no need to validate VisualEditorTree
		return nil
	}

	// Basic validation for the tree root kind
	if r.VisualEditorTree == nil || r.VisualEditorTree.GetType() != MJMLComponentMjml {
		return fmt.Errorf("invalid compile template request: visual_editor_tree must have type 'mjml'")
	}
	if r.VisualEditorTree.GetChildren() == nil {
		return fmt.Errorf("invalid compile template request: visual_editor_tree root block must have children")
	}

	return nil
}

// CompileTemplateResponse represents the response from compiling a template
type CompileTemplateResponse struct {
	Success        bool        `json:"success"`
	MJML           *string     `json:"mjml,omitempty"`            // Pointer, omit if nil
	HTML           *string     `json:"html,omitempty"`            // Pointer, omit if nil
	Subject        *string     `json:"subject,omitempty"`         // Rendered email subject (Liquid processed); omit if not provided in request
	SubjectPreview *string     `json:"subject_preview,omitempty"` // Rendered email subject preview (Liquid processed); omit if not provided in request
	Error          *mjml.Error `json:"error,omitempty"`           // Pointer, omit if nil
	TemplateData   MapOfAny    `json:"test_data,omitempty"`       // Effective template data used for rendering (includes the injected workspace object); omit if empty
}

// renderSubjectField applies the same Liquid rules used for the body to a header
// field such as Subject or SubjectPreview. Returns the rendered value (or the
// original when Liquid processing is skipped) and any error wrapped as *mjml.Error
// so the caller can surface it on the response.
func renderSubjectField(value *string, data MapOfAny, channel string, preserveLiquid bool, contextLabel string) (*string, *mjml.Error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	if preserveLiquid || channel == "web" || len(data) == 0 {
		v := *value
		return &v, nil
	}
	rendered, err := ProcessLiquidTemplate(*value, data, contextLabel)
	if err != nil {
		return nil, &mjml.Error{Message: err.Error()}
	}
	return &rendered, nil
}

// GenerateEmailRedirectionEndpoint generates the email redirection endpoint URL
// Uses encrypted path tokens (/r/{token}) to avoid pixel blocker detection.
// Falls back to legacy query params (/visit?mid=...) if encryption fails.
func GenerateEmailRedirectionEndpoint(workspaceID string, messageID string, apiEndpoint string, destinationURL string, sentTimestamp int64) string {
	// Try encrypted format: /r/{token}
	plaintext := fmt.Sprintf("%s\n%s\n%d\n%s", messageID, workspaceID, sentTimestamp, destinationURL)
	token, err := crypto.EncryptTrackingToken(plaintext)
	if err == nil {
		return fmt.Sprintf("%s/r/%s", apiEndpoint, token)
	}

	// Fallback to legacy query params
	encodedMID := url.QueryEscape(messageID)
	encodedWID := url.QueryEscape(workspaceID)
	encodedURL := url.QueryEscape(destinationURL)
	return fmt.Sprintf("%s/visit?mid=%s&wid=%s&ts=%d&url=%s",
		apiEndpoint, encodedMID, encodedWID, sentTimestamp, encodedURL)
}

// GenerateHTMLOpenTrackingPixel generates the HTML for the open tracking pixel.
// Uses encrypted path tokens (/t/{token}) to avoid pixel blocker detection.
// Falls back to legacy query params (/opens?mid=...) if encryption fails.
// Veridian fork (L4) : la cellule annule sa hauteur de ligne et l'image quitte la ligne de base, sinon
// chaque e-mail ouvert réservait une ligne de texte vide sous son contenu (port du correctif v39.1, AGPL).
// L'effondrement reste en CSS : les attributs width/height sont l'empreinte que les bloqueurs de pixel reconnaissent.
func GenerateHTMLOpenTrackingPixel(workspaceID string, messageID string, apiEndpoint string, sentTimestamp int64) string {
	// Try encrypted format: /t/{token}
	plaintext := fmt.Sprintf("%s\n%s\n%d", messageID, workspaceID, sentTimestamp)
	token, err := crypto.EncryptTrackingToken(plaintext)
	var pixelURL string
	if err == nil {
		pixelURL = fmt.Sprintf("%s/t/%s", apiEndpoint, token)
	} else {
		// Fallback to legacy query params
		encodedMID := url.QueryEscape(messageID)
		encodedWID := url.QueryEscape(workspaceID)
		pixelURL = fmt.Sprintf("%s/opens?mid=%s&wid=%s&ts=%d",
			apiEndpoint, encodedMID, encodedWID, sentTimestamp)
	}
	return fmt.Sprintf(`<table border="0" cellpadding="0" cellspacing="0" role="presentation" width="100%%"><tr><td style="line-height:0px;font-size:0px;mso-line-height-rule:exactly;"><img src="%s" alt="" style="border:0;display:block;outline:none;text-decoration:none;"></td></tr></table>`, pixelURL)
}

// CompileTemplate compiles a visual editor tree to MJML and HTML
func CompileTemplate(req CompileTemplateRequest) (resp *CompileTemplateResponse, err error) {
	var mjmlString string

	// Render Subject and SubjectPreview through Liquid before any body work, so
	// the rendered values can be returned even when the body fails to compile.
	// A malformed Liquid expression in the subject short-circuits the response.
	// Veridian fork (sécurité) : les données (contact, flux, API) sont du texte, jamais
	// du modèle. Leurs accolades sont neutralisées pour tout le rendu (Liquid par bloc,
	// passe sur la chaîne entière, spintax) et remises avant la compilation MJML.
	// Voir veridian_liquid_data.go.
	neutralized := !req.PreserveLiquid && req.Channel != "web" && len(req.TemplateData) > 0
	if neutralized {
		req.TemplateData = veridianNeutralizeData(req.TemplateData)
	}
	renderedSubject, subjectErr := renderSubjectField(req.Subject, req.TemplateData, req.Channel, req.PreserveLiquid, "email_subject")
	if subjectErr != nil {
		return &CompileTemplateResponse{Success: false, Error: subjectErr}, nil
	}
	renderedSubjectPreview, previewErr := renderSubjectField(req.SubjectPreview, req.TemplateData, req.Channel, req.PreserveLiquid, "email_subject_preview")
	if previewErr != nil {
		return &CompileTemplateResponse{Success: false, Subject: veridianRestorePtr(renderedSubject), Error: previewErr}, nil
	}

	// Veridian fork (cold outbound) — résolution spintax du sujet et du preview,
	// même graine déterministe que le corps. Un sujet identique sur 500 envois est
	// une signature spam aussi forte qu'un corps identique. Appliqué APRÈS le rendu
	// Liquid du champ. Seed vide = no-op strict. nil-safe (renderSubjectField peut
	// renvoyer nil quand le champ est absent).
	if req.VeridianSpintaxSeed != "" {
		if renderedSubject != nil {
			v := veridianApplySpintax(*renderedSubject, req.VeridianSpintaxSeed)
			renderedSubject = &v
		}
		if renderedSubjectPreview != nil {
			v := veridianApplySpintax(*renderedSubjectPreview, req.VeridianSpintaxSeed)
			renderedSubjectPreview = &v
		}
	}

	if neutralized {
		renderedSubject = veridianRestorePtr(renderedSubject)
		renderedSubjectPreview = veridianRestorePtr(renderedSubjectPreview)
	}

	// If MjmlSource is provided (code mode), use it directly.
	// Note: Channel filtering is not applied in code mode — code mode users
	// control their own MJML structure directly.
	if req.MjmlSource != nil && *req.MjmlSource != "" {
		mjmlString = *req.MjmlSource

		// Apply subject_preview override in MJML source before Liquid processing
		if req.SubjectPreviewOverride != nil && *req.SubjectPreviewOverride != "" {
			mjmlString = overrideMjPreviewInSource(mjmlString, *req.SubjectPreviewOverride)
		}

		// Process Liquid templates if template data is provided and PreserveLiquid is false
		if !req.PreserveLiquid && len(req.TemplateData) > 0 {
			processed, err := ProcessLiquidTemplate(mjmlString, req.TemplateData, "mjml-source")
			if err != nil {
				return &CompileTemplateResponse{
					Success:        false,
					Subject:        renderedSubject,
					SubjectPreview: renderedSubjectPreview,
					Error: &mjml.Error{
						Message: err.Error(),
					},
				}, nil
			}
			mjmlString = processed
		}
	} else {
		// Visual editor mode: convert JSON tree to MJML

		tree := req.VisualEditorTree

		// Apply subject_preview override in the tree before conversion
		if req.SubjectPreviewOverride != nil && *req.SubjectPreviewOverride != "" {
			updateBlockContent(tree, MJMLComponentMjPreview, *req.SubjectPreviewOverride)
		}

		// If PreserveLiquid is true, skip all Liquid processing and return raw MJML
		// This is used for MJML export where we want to preserve Liquid syntax like {{contact.external_id}}
		if req.PreserveLiquid {
			mjmlString = ConvertJSONToMJMLRaw(tree)
		} else {
			// Prepare template data JSON string
			// Note: Web channel doesn't use template data (no contact personalization)
			var templateDataStr string
			if len(req.TemplateData) > 0 && req.Channel != "web" {
				jsonDataBytes, err := json.Marshal(req.TemplateData)
				if err != nil {
					return &CompileTemplateResponse{
						Success:        false,
						MJML:           nil,
						HTML:           nil,
						Subject:        renderedSubject,
						SubjectPreview: renderedSubjectPreview,
						Error: &mjml.Error{
							Message: fmt.Sprintf("failed to marshal template data: %v", err),
						},
					}, nil
				}
				templateDataStr = string(jsonDataBytes)
			}

			// Compile tree to MJML using our pkg/mjml function with template data
			if templateDataStr != "" {
				var err error
				mjmlString, err = ConvertJSONToMJMLWithData(tree, templateDataStr)
				if err != nil {
					return &CompileTemplateResponse{
						Success:        false,
						MJML:           nil,
						HTML:           nil,
						Subject:        renderedSubject,
						SubjectPreview: renderedSubjectPreview,
						Error: &mjml.Error{
							Message: err.Error(),
						},
					}, nil
				}
			} else {
				mjmlString = ConvertJSONToMJML(tree)
			}
		}
	}

	// Whole-string Liquid pass for visual editor mode.
	// Processes raw Liquid from mj-liquid blocks. Existing block content was already
	// Liquid-processed per-block during tree walk, so the second pass is a no-op for them.
	if req.MjmlSource == nil && !req.PreserveLiquid && len(req.TemplateData) > 0 && req.Channel != "web" {
		processed, liquidErr := ProcessLiquidTemplate(mjmlString, req.TemplateData, "visual-editor-whole")
		if liquidErr != nil {
			return &CompileTemplateResponse{
				Success:        false,
				Subject:        renderedSubject,
				SubjectPreview: renderedSubjectPreview,
				Error:          &mjml.Error{Message: liquidErr.Error()},
			}, nil
		}
		mjmlString = processed
	}

	// For visual editor mode: if subject_preview override was requested but the tree
	// didn't contain an mj-preview block, fall back to injecting it in the MJML string.
	if req.MjmlSource == nil && req.SubjectPreviewOverride != nil && *req.SubjectPreviewOverride != "" {
		if !mjPreviewTagRegexp.MatchString(mjmlString) {
			mjmlString = overrideMjPreviewInSource(mjmlString, *req.SubjectPreviewOverride)
		}
	}

	// Veridian fork (cold outbound) — résolution spintax `{a|b|c}` par destinataire.
	// Placée APRÈS tout rendu Liquid (les `{{ }}` sont déjà remplacés) et AVANT le
	// preprocessing/rendu MJML, sur la string MJML complète : un seul point
	// d'invocation, zone disjointe du tracking de liens (TrackLinks plus bas).
	// Seed vide = no-op strict (non-régression). Voir veridian_spintax_apply.go.
	mjmlString = veridianApplySpintax(mjmlString, req.VeridianSpintaxSeed)
	if neutralized {
		mjmlString = veridianRestoreBraces(mjmlString)
	}

	// Preprocess MJML to fix HTML vs XML incompatibilities
	// gomjml uses a strict XML parser that doesn't accept HTML void tags (<br>) or HTML entities (&nbsp;)
	preprocessedMjml := preprocessMjmlForXML(mjmlString)

	// Compile MJML to HTML using gomjml library
	htmlResult, err := mjml.Render(preprocessedMjml)
	if err != nil {
		// Return the response struct with Success=false and the Error details
		return &CompileTemplateResponse{
			Success:        false,
			MJML:           &mjmlString, // Include original MJML for context if desired
			HTML:           nil,
			Subject:        renderedSubject,
			SubjectPreview: renderedSubjectPreview,
			Error: &mjml.Error{
				Message: err.Error(),
			},
		}, nil
	}

	// Decode HTML entities in href attributes to fix broken URLs with query parameters
	// The MJML-to-HTML compiler doesn't always decode &amp; back to & in href attributes
	htmlResult = decodeHTMLEntitiesInURLAttributes(htmlResult)

	// Skip tracking for web channel
	if req.Channel == "web" {
		return &CompileTemplateResponse{
			Success:        true,
			MJML:           &mjmlString,
			HTML:           &htmlResult, // No tracking applied for web
			Subject:        renderedSubject,
			SubjectPreview: renderedSubjectPreview,
			Error:          nil,
		}, nil
	}

	// Apply link tracking to the HTML output (email channel only).
	// Tracking failures are usually user-content issues (e.g. malformed href in
	// an mj-button), so surface them as structured compile errors like every
	// other failure path rather than as a Go error — that way the rendered
	// subject/subject_preview reach the client instead of being dropped.
	trackedHTML, err := TrackLinks(htmlResult, req.TrackingSettings)
	if err != nil {
		return &CompileTemplateResponse{
			Success:        false,
			MJML:           &mjmlString,
			HTML:           nil,
			Subject:        renderedSubject,
			SubjectPreview: renderedSubjectPreview,
			Error: &mjml.Error{
				Message: err.Error(),
			},
		}, nil
	}

	// Return successful response
	return &CompileTemplateResponse{
		Success:        true,
		MJML:           &mjmlString,
		HTML:           &trackedHTML,
		Subject:        renderedSubject,
		SubjectPreview: renderedSubjectPreview,
		Error:          nil,
	}, nil
}

// decodeHTMLEntitiesInURLAttributes decodes HTML entities (&amp;, &quot;, etc.)
// in href, src, and other URL attributes to ensure clickable links work correctly.
// The MJML-to-HTML compiler doesn't always decode these entities properly in attributes,
// which breaks URLs with query parameters (e.g., ?action=confirm&email=... becomes &amp;email=...)
func decodeHTMLEntitiesInURLAttributes(html string) string {
	// Pattern matches href="...", src="...", action="..." attributes
	// Captures: (attribute=") (url content) (")
	urlAttrRegex := regexp.MustCompile(`((?:href|src|action)=["'])([^"']+)(["'])`)

	return urlAttrRegex.ReplaceAllStringFunc(html, func(match string) string {
		parts := urlAttrRegex.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match // Return original if parsing fails
		}

		beforeURL := parts[1]  // href=" or src=" or action="
		encodedURL := parts[2] // the URL with HTML entities
		afterURL := parts[3]   // closing "

		// Decode common HTML entities that appear in URLs
		// Note: We only decode entities that are safe to decode in URL context
		decodedURL := encodedURL
		decodedURL = strings.ReplaceAll(decodedURL, "&amp;", "&")
		decodedURL = strings.ReplaceAll(decodedURL, "&quot;", "\"")
		decodedURL = strings.ReplaceAll(decodedURL, "&#39;", "'")
		decodedURL = strings.ReplaceAll(decodedURL, "&lt;", "<")
		decodedURL = strings.ReplaceAll(decodedURL, "&gt;", ">")

		return beforeURL + decodedURL + afterURL
	})
}

// openPixelEnabled retourne si le pixel d'ouverture doit être inséré. Veridian
// fork : nil EnableOpenPixel = comportement upstream (le pixel suit
// EnableTracking) ; non-nil = override explicite par classe de provider,
// indépendant d'EnableTracking (cf. VeridianResolveOpenPixel).
func (t TrackingSettings) openPixelEnabled() bool {
	if t.EnableOpenPixel != nil {
		return *t.EnableOpenPixel
	}
	return t.EnableTracking
}

func TrackLinks(htmlString string, trackingSettings TrackingSettings) (updatedHTML string, err error) {
	// If tracking is disabled, no pixel to insert, and no UTM parameters to add,
	// return original HTML. (Veridian fork: openPixelEnabled() peut forcer
	// l'insertion du pixel même quand EnableTracking est false — cas pixel ON sur
	// petit provider sans réécriture de liens.)
	if !trackingSettings.EnableTracking && !trackingSettings.openPixelEnabled() &&
		trackingSettings.UTMSource == "" &&
		trackingSettings.UTMMedium == "" && trackingSettings.UTMCampaign == "" &&
		trackingSettings.UTMContent == "" && trackingSettings.UTMTerm == "" {
		return htmlString, nil
	}

	// Use regex to find and replace href attributes in <a> tags
	// This regex matches: <a ...href="url"... > or <a ...href='url'... >
	hrefRegex := regexp.MustCompile(`(<a[^>]*\s+href=["'])([^"']+)(["'][^>]*>)`)

	updatedHTML = hrefRegex.ReplaceAllStringFunc(htmlString, func(match string) string {
		// Extract the parts: opening tag with href=", URL, closing " and rest of tag
		parts := hrefRegex.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match // Return original if parsing fails
		}

		beforeURL := parts[1]   // <a ...href="
		originalURL := parts[2] // the URL
		afterURL := parts[3]    // "...>

		// Skip tracking for special protocol links (mailto, tel, sms, etc.)
		// These should not be wrapped in a redirect as it breaks their functionality
		if isNonTrackableURL(originalURL) {
			return match // Return original link unchanged
		}

		// Append UTM parameters to the destination URL
		destinationURL := trackingSettings.applyUTMParameters(originalURL)
		trackedURL := destinationURL

		if trackingSettings.EnableTracking {
			// Use current Unix timestamp (seconds) for bot detection.
			// The UTM-augmented destination URL is what gets encrypted into the
			// token, so the redirect target preserves the UTM parameters.
			sentTimestamp := time.Now().Unix()
			trackedURL = GenerateEmailRedirectionEndpoint(trackingSettings.WorkspaceID, trackingSettings.MessageID, trackingSettings.Endpoint, destinationURL, sentTimestamp)
		}

		// Return the updated tag
		return beforeURL + trackedURL + afterURL
	})

	if trackingSettings.openPixelEnabled() {
		// Insert tracking pixel before </body>. The pixel is wrapped in a <table>
		// by GenerateHTMLOpenTrackingPixel to look like a structural layout element
		// rather than a standalone tracking pixel.
		sentTimestamp := time.Now().Unix()
		trackingPixel := GenerateHTMLOpenTrackingPixel(trackingSettings.WorkspaceID, trackingSettings.MessageID, trackingSettings.Endpoint, sentTimestamp)

		bodyCloseRegex := regexp.MustCompile(`(?i)(<\/body>)`)
		if bodyCloseRegex.MatchString(updatedHTML) {
			updatedHTML = bodyCloseRegex.ReplaceAllString(updatedHTML, trackingPixel+"$1")
		} else {
			updatedHTML = updatedHTML + trackingPixel
		}
	}

	return updatedHTML, nil
}

// mjPreviewTagRegexp matches <mj-preview>...</mj-preview> in MJML source.
var mjPreviewTagRegexp = regexp.MustCompile(`(?is)(<mj-preview\s*>)([\s\S]*?)(</mj-preview\s*>)`)

// mjHeadTagRegexp matches the opening <mj-head...> tag.
var mjHeadTagRegexp = regexp.MustCompile(`(?i)<mj-head[^>]*>`)

// mjmlRootTagRegexp matches the opening <mjml...> tag.
var mjmlRootTagRegexp = regexp.MustCompile(`(?i)<mjml[^>]*>`)

// overrideMjPreviewInSource replaces or injects <mj-preview> in raw MJML source.
// Content is XML-escaped for safe insertion.
// Fallback order: replace existing → inject after <mj-head> → create <mj-head> after <mjml>.
func overrideMjPreviewInSource(mjmlSource string, previewText string) string {
	escaped := escapeXMLContent(previewText)

	// Replace existing <mj-preview> content
	if mjPreviewTagRegexp.MatchString(mjmlSource) {
		return mjPreviewTagRegexp.ReplaceAllString(mjmlSource, "${1}"+escapeRegexpReplacement(escaped)+"${3}")
	}

	// No <mj-preview> — inject after <mj-head>
	newTag := "<mj-preview>" + escaped + "</mj-preview>"
	loc := mjHeadTagRegexp.FindStringIndex(mjmlSource)
	if loc != nil {
		return mjmlSource[:loc[1]] + "\n    " + newTag + mjmlSource[loc[1]:]
	}

	// No <mj-head> — create one after <mjml>
	loc = mjmlRootTagRegexp.FindStringIndex(mjmlSource)
	if loc != nil {
		return mjmlSource[:loc[1]] + "\n  <mj-head>\n    " + newTag + "\n  </mj-head>" + mjmlSource[loc[1]:]
	}

	// No <mjml> tag found; return as-is
	return mjmlSource
}

// escapeXMLContent escapes &, <, > for safe insertion as XML element text content.
func escapeXMLContent(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// escapeRegexpReplacement escapes $ signs so they are treated literally by ReplaceAllString.
func escapeRegexpReplacement(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// updateBlockContent traverses the block tree and sets the content of all blocks
// matching the given type. Used to override mj-preview content before compilation.
func updateBlockContent(block EmailBlock, blockType MJMLComponentType, content string) {
	if block == nil {
		return
	}
	if block.GetType() == blockType {
		block.SetContent(&content)
	}
	for _, child := range block.GetChildren() {
		updateBlockContent(child, blockType, content)
	}
}
