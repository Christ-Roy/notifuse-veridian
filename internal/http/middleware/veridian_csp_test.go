package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cspHeaders(t *testing.T, mode, path string) http.Header {
	t.Helper()
	t.Setenv("VERIDIAN_CSP_MODE", mode)
	rec := httptest.NewRecorder()
	VeridianSecurityHeadersMiddleware(noopHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Header()
}

// Par défaut (et sur une valeur inconnue) : Report-Only ; enforce : bloquante ; off : rien. Jamais sur /api/*.
func TestVeridianCSP_Modes(t *testing.T) {
	for _, mode := range []string{"", "report", "nimporte"} {
		h := cspHeaders(t, mode, "/console/")
		if h.Get("Content-Security-Policy-Report-Only") != VeridianCSPPolicy() || h.Get("Content-Security-Policy") != "" {
			t.Fatalf("mode %q : %v", mode, h)
		}
		if h.Get("Reporting-Endpoints") != `csp="/api/veridian/csp-report"` {
			t.Fatalf("mode %q : Reporting-Endpoints %q", mode, h.Get("Reporting-Endpoints"))
		}
	}
	h := cspHeaders(t, "enforce", "/console/workspace/x/contacts")
	if h.Get("Content-Security-Policy") != VeridianCSPPolicy() || h.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("enforce : %v", h)
	}
	if h := cspHeaders(t, "off", "/console/"); h.Get("Content-Security-Policy") != "" || h.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("off : %v", h)
	}
	if h := cspHeaders(t, "enforce", "/api/contacts.list"); h.Get("Content-Security-Policy") != "" || h.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("/api/* ne porte pas la CSP des pages : %v", h)
	}
}

// Le rempart : script-src sans inline ni eval, objets interdits, base et cadres bornés, rapports câblés.
func TestVeridianCSPPolicy_SansInlineNiEval(t *testing.T) {
	p := VeridianCSPPolicy()
	var script string
	for _, d := range strings.Split(p, ";") {
		if d = strings.TrimSpace(d); strings.HasPrefix(d, "script-src ") {
			script = d
		}
	}
	if script == "" || strings.Contains(script, "unsafe-inline") || strings.Contains(script, "unsafe-eval") || strings.Contains(script, "*") || strings.Contains(script, " https: ") || strings.HasSuffix(script, " https:") {
		t.Fatalf("script-src trop large : %q", script)
	}
	for _, want := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'self'", "frame-ancestors 'self'", "report-uri /api/veridian/csp-report", "report-to csp"} {
		if !strings.Contains(p, want) {
			t.Fatalf("directive manquante %q dans %q", want, p)
		}
	}
}

// Page rendue côté serveur : sa politique remplace la générique, dans le mode courant, rapports compris.
func TestVeridianSetPageCSP_RemplaceEtSuitLeMode(t *testing.T) {
	for mode, header := range map[string]string{"": "Content-Security-Policy-Report-Only", "enforce": "Content-Security-Policy"} {
		t.Setenv("VERIDIAN_CSP_MODE", mode)
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Security-Policy", VeridianCSPPolicy())
		rec.Header().Set("Content-Security-Policy-Report-Only", VeridianCSPPolicy())
		VeridianSetPageCSP(rec, "default-src 'none'; script-src 'nonce-abc'")
		got := rec.Header().Values(header)
		if len(got) != 1 || !strings.HasPrefix(got[0], "default-src 'none'; script-src 'nonce-abc'") || !strings.Contains(got[0], "report-uri /api/veridian/csp-report") {
			t.Fatalf("mode %q : %v", mode, rec.Header())
		}
		other := "Content-Security-Policy"
		if header == other {
			other = "Content-Security-Policy-Report-Only"
		}
		if rec.Header().Get(other) != "" {
			t.Fatalf("mode %q : la politique générique reste en %s", mode, other)
		}
	}
}

// Nonce : neuf à chaque appel, assez long, et seulement [A-Za-z0-9_-] (html/template échappait un « + » en &#43;, test
// instable vu au pre-push du 26/11 : il ne passait que si le tirage n'en contenait pas).
func TestVeridianCSPNonce(t *testing.T) {
	vus := map[string]bool{}
	for i := 0; i < 200; i++ {
		n := VeridianCSPNonce()
		if len(n) < 20 || strings.Trim(n, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" || vus[n] {
			t.Fatalf("nonce %q : trop court, caractère échappable par html/template, ou répété", n)
		}
		vus[n] = true
	}
}
