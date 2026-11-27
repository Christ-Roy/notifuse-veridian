package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
)

// CSP de la console et du centre de notifications (VER-265, modèle Veridian relevé sur Academy le 26/11/2026,
// securite/CSP-MODELE.md). SPA statique (Vite) : script-src 'self' SANS 'unsafe-inline' ni nonce.
//
// Besoins mesurés de la console (inventaire du 26/11) :
//   - Monaco (éditeur de gabarits) : servi par la console depuis /console/monaco/vs (VER-265, 27/11 : plus de cdn.jsdelivr.net,
//     qui sert n'importe quel paquet npm et rendait script-src contournable) ; ses workers en blob:.
//   - style 'unsafe-inline' : Ant Design pose ses styles à l'exécution (CSS-in-JS) et React des attributs style.
//   - img/font/connect https: : aperçus de mails (images et polices des clients), gestionnaire de fichiers vers le S3
//     que chaque client choisit (endpoint libre). Le rempart contre le XSS est script-src, pas ces directives.
//   - frame-ancestors 'self' : comme X-Frame-Options SAMEORIGIN (aperçus Notifuse dans Notifuse).
//
// Mode par VERIDIAN_CSP_MODE : « report » (défaut, Content-Security-Policy-Report-Only), « enforce », « off ».
// Les violations arrivent sur /api/veridian/csp-report (journal csp_violation). Les réponses /api/* n'en portent pas :
// ce sont des données, et certaines routes posent leur propre CSP (corps de mail : sandbox; default-src 'none').

// VeridianCSPReportPath : point de réception des rapports de violation.
const VeridianCSPReportPath = "/api/veridian/csp-report"

// VeridianCSPPolicy rend la politique servie.
func VeridianCSPPolicy() string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"worker-src 'self' blob:",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' data: https:",
		"img-src 'self' data: blob: https:",
		"media-src 'self' https:",
		"connect-src 'self' https:",
		"frame-src 'self' blob: https://app.veridian.site",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self' https://app.veridian.site",
		"frame-ancestors 'self'",
		"report-uri " + VeridianCSPReportPath,
		"report-to csp",
	}, "; ")
}

func veridianCSPMode() string {
	switch m := strings.ToLower(strings.TrimSpace(os.Getenv("VERIDIAN_CSP_MODE"))); m {
	case "enforce", "off":
		return m
	}
	return "report"
}

// veridianSetCSP pose la CSP (ou sa version Report-Only) sur les pages ; rien sur /api/*.
func veridianSetCSP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return
	}
	header := "Content-Security-Policy-Report-Only"
	switch veridianCSPMode() {
	case "off":
		return
	case "enforce":
		header = "Content-Security-Policy"
	}
	w.Header().Set(header, VeridianCSPPolicy())
	w.Header().Set("Reporting-Endpoints", `csp="`+VeridianCSPReportPath+`"`)
}

// VeridianCSPNonce rend un nonce neuf (128 bits) pour une page rendue par le serveur avec un script inline.
func VeridianCSPNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b) // [A-Za-z0-9_-] : admis par la grammaire CSP, jamais échappé par html/template (un « + » devenait &#43;)
}

// VeridianSetPageCSP remplace la CSP générique par la politique propre d'une page rendue côté serveur (même mode
// VERIDIAN_CSP_MODE, mêmes rapports). Mesuré le 26/11/2026 : /veridian/auto-login pose le jeton de session par un script
// inline ; sous la CSP de la console (enforce), la connexion par le Hub a été coupée 3 min. Une telle page déclare
// son script par nonce, ici.
func VeridianSetPageCSP(w http.ResponseWriter, policy string) {
	h := w.Header()
	h.Del("Content-Security-Policy")
	h.Del("Content-Security-Policy-Report-Only")
	header := "Content-Security-Policy-Report-Only"
	switch veridianCSPMode() {
	case "off":
		return
	case "enforce":
		header = "Content-Security-Policy"
	}
	h.Set(header, policy+"; report-uri "+VeridianCSPReportPath+"; report-to csp")
	h.Set("Reporting-Endpoints", `csp="`+VeridianCSPReportPath+`"`)
}
