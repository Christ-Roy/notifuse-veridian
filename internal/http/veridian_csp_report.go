package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianCSPReportHandler reçoit les rapports de violation CSP des navigateurs (VER-265) et les journalise en
// `csp_violation`, une ligne par violation : directive, ressource bloquée et page, réduites à origine + chemin
// (query, fragment et extrait de code retirés : ni jeton, ni donnée de contact dans le journal). Toujours 204,
// jamais d'erreur renvoyée au navigateur. Corps borné à 16 Ko ; le rate-limit global de l'API s'applique.
type VeridianCSPReportHandler struct {
	Logger logger.Logger
}

// RegisterRoutes pose la route publique (un navigateur ne s'authentifie pas pour rapporter).
func (h *VeridianCSPReportHandler) RegisterRoutes(mux *http.ServeMux) {
	// Chemin en littéral (le contrôle de couverture CLI lit le routeur tel qu'écrit) ; égal à middleware.VeridianCSPReportPath (testé).
	mux.HandleFunc("POST /api/veridian/csp-report", h.handle)
}

type veridianCSPViolation struct {
	Directive string
	Blocked   string
	Document  string
	Source    string
	Line      int
	Mode      string
}

func (h *VeridianCSPReportHandler) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	for _, v := range parseVeridianCSPReports(body) {
		h.Logger.WithFields(map[string]interface{}{
			"directive": v.Directive, "blocked": v.Blocked, "document": v.Document,
			"source": v.Source, "line": v.Line, "disposition": v.Mode,
		}).Warn("csp_violation")
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseVeridianCSPReports lit les deux formats : application/csp-report ({"csp-report":{...}}, report-uri) et
// application/reports+json ([{"type":"csp-violation","body":{...}}], report-to). Un corps illisible ne rend rien.
func parseVeridianCSPReports(body []byte) []veridianCSPViolation {
	var out []veridianCSPViolation
	var legacy struct {
		R map[string]interface{} `json:"csp-report"`
	}
	if json.Unmarshal(body, &legacy) == nil && legacy.R != nil {
		m := legacy.R
		out = append(out, veridianCSPViolation{
			Directive: veridianCSPStr(m["effective-directive"], m["violated-directive"]), Blocked: veridianCSPScrub(veridianCSPStr(m["blocked-uri"])),
			Document: veridianCSPScrub(veridianCSPStr(m["document-uri"])), Source: veridianCSPScrub(veridianCSPStr(m["source-file"])),
			Line: veridianCSPNum(m["line-number"]), Mode: veridianCSPStr(m["disposition"]),
		})
		return out
	}
	var reports []struct {
		Type string                 `json:"type"`
		Body map[string]interface{} `json:"body"`
	}
	if json.Unmarshal(body, &reports) != nil {
		return nil
	}
	for _, rep := range reports {
		if rep.Type != "csp-violation" || rep.Body == nil {
			continue
		}
		m := rep.Body
		out = append(out, veridianCSPViolation{
			Directive: veridianCSPStr(m["effectiveDirective"]), Blocked: veridianCSPScrub(veridianCSPStr(m["blockedURL"])),
			Document: veridianCSPScrub(veridianCSPStr(m["documentURL"])), Source: veridianCSPScrub(veridianCSPStr(m["sourceFile"])),
			Line: veridianCSPNum(m["lineNumber"]), Mode: veridianCSPStr(m["disposition"]),
		})
		if len(out) >= 50 {
			break
		}
	}
	return out
}

// veridianCSPScrub réduit une URL à origine + chemin ; garde les mots-clés du navigateur (inline, eval, data, blob...).
func veridianCSPScrub(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, k := range []string{"data:", "blob:", "filesystem:"} {
		if strings.HasPrefix(s, k) {
			return strings.TrimSuffix(k, ":")
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// Mot-clé du navigateur (inline, eval, wasm-eval, trusted-types-sink...) ou chemin relatif : sans query ni fragment.
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		if len(s) > 60 {
			s = s[:60]
		}
		return s
	}
	p := u.EscapedPath()
	if len(p) > 200 {
		p = p[:200]
	}
	return u.Scheme + "://" + u.Host + p
}

func veridianCSPStr(vals ...interface{}) string {
	for _, v := range vals {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func veridianCSPNum(v interface{}) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
