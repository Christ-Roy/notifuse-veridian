package middleware

import (
	"net/http"
	"os"
	"strings"
)

// VeridianSecurityHeadersMiddleware remplace le CORSMiddleware upstream pour
// fixer 2 problèmes identifiés par le pentest doomsday du 2026-05-20 :
//
//  1. CORS `*` + `Credentials: true` : combo interdit par spec CORS mais
//     accepté par certains browsers anciens → permet à n'importe quel site
//     malveillant d'envoyer des requêtes authentifiées avec les cookies du
//     user. Fix : allowlist explicite, on echo l'Origin uniquement si elle
//     match. Pas de match → pas d'header ACAO → browser bloque.
//
//  2. Security headers manquants (5/5) : HSTS, X-Frame-Options,
//     X-Content-Type-Options, Referrer-Policy. Pas de CSP : trop risqué de
//     casser la console UI sans audit dédié — on garde un report-only pour
//     plus tard si Robert le demande.
//
// Convention Veridian (cf. ../../../CLAUDE.md §veridian-override) : ce
// middleware est dans le même package `middleware` mais préfixé
// `veridian_*.go` pour ne pas patcher l'upstream `cors.go`. Le câblage se
// fait depuis `internal/app/app.go` qui remplace `CORSMiddleware(handler)`
// par `VeridianSecurityHeadersMiddleware(handler)`.
//
// Note sur le HMAC server-to-server : le Hub appelle Notifuse sans browser
// (curl/Go client direct). CORS ne s'applique PAS sur ces appels (pas
// d'Origin header → on ne renvoie aucun ACAO, mais le HMAC bypass auth donc
// l'appel passe quand même). Zéro impact sur le flow Hub↔Notifuse.
func VeridianSecurityHeadersMiddleware(next http.Handler) http.Handler {
	allowlist := defaultCORSAllowlist()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// === 1. CORS allowlist ===
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, allowlist) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			// Vary: Origin évite que les caches HTTP partagés (CDN, browser)
			// servent une réponse CORS d'une origin à une autre.
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		// Si origin vide (server-to-server) ou pas allowlist : on ne pose
		// aucun header ACAO → le browser bloquera la requête côté client.
		// Les appels server-to-server (HMAC Hub) continuent de fonctionner.

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization, X-Veridian-Hub-Signature, X-Veridian-Timestamp")

		// === 2. Security headers (toujours appliqués) ===
		//
		// HSTS : force HTTPS 180 jours. PAS de `preload` (réversible si on
		// doit revenir HTTP pour debug). PAS d'`includeSubDomains` (les
		// sous-domaines Veridian sont gérés indépendamment).
		w.Header().Set("Strict-Transport-Security", "max-age=15552000")

		// nosniff : empêche le browser de "deviner" le content-type.
		// Aucun impact UI.
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// SAMEORIGIN (pas DENY) : autorise les iframes Notifuse→Notifuse
		// (preview MJML, notification_center embed) mais bloque l'embed
		// dans un site tiers (clickjacking).
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")

		// strict-origin-when-cross-origin = défaut Chrome moderne.
		// Évite de leaker l'URL complète vers un domaine externe.
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// CSP volontairement absente — voir doc en tête du fichier.

		// === 3. Preflight ===
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// defaultCORSAllowlist retourne la liste d'origins autorisées. Lu depuis
// CORS_ALLOW_ORIGIN env var (comma-separated) avec fallback aux 3 domaines
// Veridian + localhost dev pour la console Vite.
//
// Format env : `https://a.com,https://b.com` (pas d'espaces).
func defaultCORSAllowlist() []string {
	env := strings.TrimSpace(os.Getenv("CORS_ALLOW_ORIGIN"))
	if env != "" && env != "*" {
		raw := strings.Split(env, ",")
		out := make([]string, 0, len(raw))
		for _, o := range raw {
			o = strings.TrimSpace(o)
			if o != "" {
				out = append(out, o)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	// Fallback : domaines Veridian + dev local (Vite ports 3000/5173).
	return []string{
		"https://notifuse.app.veridian.site",
		"https://notifuse.staging.veridian.site",
		"http://localhost:3000",
		"http://localhost:5173",
	}
}

// isAllowedOrigin compare l'Origin request à l'allowlist en strict match.
// Pas de wildcard, pas de regex : on évite les erreurs subtiles type
// "https://evil.notifuse.app.veridian.site.evil.com" qui matcherait un suffix.
func isAllowedOrigin(origin string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if origin == allowed {
			return true
		}
	}
	return false
}
