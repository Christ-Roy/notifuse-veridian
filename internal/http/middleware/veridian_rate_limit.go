package middleware

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
)

// === Veridian — rate-limit GLOBAL de l'API (OWASP API4:2023) ===
//
// Notifuse va être peuplé en prod (tunnel cold outreach). Le `pkg/ratelimiter`
// upstream existe mais n'est appliqué qu'À LA MAIN, handler par handler, et
// UNIQUEMENT sur les endpoints publics non-auth (subscribe/preferences, cf.
// public_handler.go). TOUS les endpoints authentifiés JWT et HMAC (dont le
// custom Veridian cold : breakdown, attach-member, sso, automations.*) n'ont
// AUCUN rate-limit → trou API4:2023 (Unrestricted Resource Consumption).
//
// Ce middleware GLOBAL ferme le trou en defense-in-depth : il s'applique sur
// tout le préfixe /api/ (le reste — console SPA, assets, /subscribe,
// /preferences, /health, /healthz — n'est pas touché). Il limite par DEUX
// dimensions indépendantes (la plus restrictive bloque en premier, car les
// deux Allow() sont évalués) :
//
//  1. par IP client (anti-flood réseau, défaut 300 req/min)
//  2. par IDENTITÉ authentifiée best-effort (user_id du JWT OU app+workspace
//     HMAC ; défaut 600 req/min, plus large car un appelant légitime
//     authentifié fait plus de trafic qu'une IP anonyme)
//
// Si aucune identité n'est extractible (requête anonyme), seule la dimension
// IP s'applique. L'identité est lue SANS valider la signature (le JWT n'est
// vérifié que plus loin, dans RequireAuth, qui tourne à l'intérieur du mux) :
// c'est volontaire et sûr — un attaquant qui forge un user_id arbitraire se
// fait juste rate-limiter sur une clé différente, mais la dimension IP (qu'il
// ne peut pas changer aussi facilement) reste appliquée. La clé identité sert
// à éviter qu'un gros tenant légitime derrière un NAT partagé sature la limite
// IP d'un autre tenant, PAS à être une frontière de sécurité.
//
// EXEMPTIONS (jamais rate-limitées) :
//   - /api/health, /api/version, /api/tenants/{id}/health : observabilité +
//     smoke tests CI doivent toujours passer.
//   - le flux HMAC Hub→Notifuse (header X-Veridian-Hub-Signature présent) :
//     déjà protégé par signature + anti-replay 5 min ; le cron reconcile Hub
//     fait des rafales légitimes (discovery by-email, sync tenants) qu'une
//     limite trop basse casserait → faux positifs tenant_missing_app côté Hub.
//
// CONFIG (lue au câblage, pattern identique à VeridianSecurityHeadersMiddleware
// qui lit CORS_ALLOW_ORIGIN — on ne pollue PAS le struct config upstream) :
//   - VERIDIAN_API_RATE_LIMIT_ENABLED      (défaut "true" ; "false"/"0" = no-op)
//   - VERIDIAN_API_RATE_LIMIT_PER_IP        (défaut 300, req/min par IP)
//   - VERIDIAN_API_RATE_LIMIT_PER_IDENTITY  (défaut 600, req/min par identité)
//
// BEST-EFFORT : un souci de rate-limiter ne doit JAMAIS 500 une requête
// légitime. Le middleware ne fait que des opérations en mémoire et nil-checks ;
// si rl == nil, il est en passthrough pur.

const (
	// Namespaces du pkg/ratelimiter (clé = namespace:key).
	veridianRateLimitNamespaceIP       = "veridian_api:ip"
	veridianRateLimitNamespaceIdentity = "veridian_api:identity"

	// Défauts sains : large pour ne jamais gêner un usage console légitime
	// (la console fait des rafales TanStack Query au chargement d'une page),
	// mais coupe un flood / scraping authentifié ou anonyme.
	veridianRateLimitDefaultPerIP       = 300
	veridianRateLimitDefaultPerIdentity = 600

	veridianRateLimitWindow = time.Minute
)

// veridianRateLimitExemptPrefixes : préfixes /api/ jamais rate-limités
// (observabilité + smoke CI). Le reste hors /api/ n'est de toute façon pas
// inspecté (cf. la garde HasPrefix("/api/") dans le handler).
var veridianRateLimitExemptPrefixes = []string{
	"/api/health",
	"/api/version",
}

// isVeridianRateLimitExemptPath retourne true pour les paths d'observabilité
// exemptés (health/version), y compris /api/tenants/{id}/health.
func isVeridianRateLimitExemptPath(path string) bool {
	for _, prefix := range veridianRateLimitExemptPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	// /api/tenants/{id}/health (HMAC) : même logique d'observabilité.
	if strings.HasPrefix(path, "/api/tenants/") && strings.HasSuffix(path, "/health") {
		return true
	}
	return false
}

// isVeridianHMACRequest détecte le flux HMAC Hub→Notifuse via la présence de
// la signature inbound. Ces requêtes sont exemptées : déjà protégées par
// signature + anti-replay, et le cron reconcile Hub fait des rafales légitimes.
// (Header lu via r.Header.Get → insensible à la casse.)
func isVeridianHMACRequest(r *http.Request) bool {
	return r.Header.Get("X-Veridian-Hub-Signature") != ""
}

// VeridianAPIRateLimitConfig porte les seuils résolus depuis l'ENV. Exposée
// pour que le câblage app.go et les tests construisent un middleware
// déterministe sans dépendre des variables d'environnement du process.
type VeridianAPIRateLimitConfig struct {
	Enabled     bool
	PerIP       int
	PerIdentity int
	Window      time.Duration
}

// LoadVeridianAPIRateLimitConfig lit la config depuis l'environnement avec des
// défauts sains. Une valeur non parsable retombe sur le défaut (best-effort,
// jamais d'échec de boot pour un ENV mal typé).
func LoadVeridianAPIRateLimitConfig() VeridianAPIRateLimitConfig {
	cfg := VeridianAPIRateLimitConfig{
		Enabled:     true,
		PerIP:       veridianRateLimitDefaultPerIP,
		PerIdentity: veridianRateLimitDefaultPerIdentity,
		Window:      veridianRateLimitWindow,
	}

	if v := strings.TrimSpace(os.Getenv("VERIDIAN_API_RATE_LIMIT_ENABLED")); v != "" {
		// Tout sauf "false"/"0"/"no" est considéré activé (fail-safe : on
		// préfère rate-limiter par défaut que laisser le trou ouvert sur un
		// typo d'ENV).
		switch strings.ToLower(v) {
		case "false", "0", "no", "off":
			cfg.Enabled = false
		}
	}

	if n, ok := parsePositiveInt(os.Getenv("VERIDIAN_API_RATE_LIMIT_PER_IP")); ok {
		cfg.PerIP = n
	}
	if n, ok := parsePositiveInt(os.Getenv("VERIDIAN_API_RATE_LIMIT_PER_IDENTITY")); ok {
		cfg.PerIdentity = n
	}

	return cfg
}

// parsePositiveInt parse un entier strictement positif. ("", "abc", "0", "-1"
// → ok=false → on garde le défaut).
func parsePositiveInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// VeridianAPIRateLimitMiddleware construit le middleware global. Il enregistre
// ses deux policies sur le RateLimiter PARTAGÉ fourni (la même instance que les
// rate-limits publics existants — on ne crée pas de 2e goroutine de cleanup) et
// retourne un wrapper http.Handler.
//
// rl == nil OU cfg.Enabled == false → passthrough pur (no-op strict).
func VeridianAPIRateLimitMiddleware(rl *ratelimiter.RateLimiter, cfg VeridianAPIRateLimitConfig, log logger.Logger) func(http.Handler) http.Handler {
	if rl != nil && cfg.Enabled {
		rl.SetPolicy(veridianRateLimitNamespaceIP, cfg.PerIP, cfg.Window)
		rl.SetPolicy(veridianRateLimitNamespaceIdentity, cfg.PerIdentity, cfg.Window)
	}

	return func(next http.Handler) http.Handler {
		// Désactivé ou pas de limiter : on ne wrappe même pas, zéro surcoût.
		if rl == nil || !cfg.Enabled {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// On ne rate-limite QUE l'API. Tout le reste (console SPA, assets,
			// /subscribe, /preferences avec leurs propres limites fines,
			// /health, /healthz) passe sans inspection.
			if !strings.HasPrefix(path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			// Exemptions : observabilité + flux HMAC Hub (déjà protégé +
			// rafales cron reconcile légitimes).
			if isVeridianRateLimitExemptPath(path) || isVeridianHMACRequest(r) {
				next.ServeHTTP(w, r)
				return
			}

			ip := veridianRateLimitClientIP(r)

			// Dimension IP (toujours évaluée). On enregistre l'attempt via
			// Allow ; un refus → 429.
			if !rl.Allow(veridianRateLimitNamespaceIP, ip) {
				retryAfter := rl.GetRemainingWindow(veridianRateLimitNamespaceIP, ip)
				veridianWriteRateLimited(w, retryAfter)
				if log != nil {
					log.WithField("ip", ip).WithField("path", path).
						Warn("Veridian API rate limit exceeded (per-IP)")
				}
				return
			}

			// Dimension identité (best-effort). Si aucune identité extractible,
			// on ne consomme pas de quota identité (la dimension IP suffit).
			if identity := veridianRateLimitIdentity(r); identity != "" {
				if !rl.Allow(veridianRateLimitNamespaceIdentity, identity) {
					retryAfter := rl.GetRemainingWindow(veridianRateLimitNamespaceIdentity, identity)
					veridianWriteRateLimited(w, retryAfter)
					if log != nil {
						log.WithField("identity", identity).WithField("path", path).
							Warn("Veridian API rate limit exceeded (per-identity)")
					}
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// veridianWriteRateLimited écrit une réponse 429 + Retry-After (secondes).
// Retry-After omis si <= 0 (rien à attendre, cas limite).
func veridianWriteRateLimited(w http.ResponseWriter, retryAfterSeconds int) {
	if retryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "Too many requests. Please slow down and retry later.",
		"code":  "rate_limited",
	})
}

// veridianRateLimitClientIP extrait l'IP client. Même logique que getClientIP
// du public_handler (X-Forwarded-For premier hop → X-Real-IP → RemoteAddr sans
// port), dupliquée ici pour rester dans le package middleware sans dépendance
// circulaire vers internal/http.
func veridianRateLimitClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	ip := r.RemoteAddr
	if colon := strings.LastIndex(ip, ":"); colon != -1 {
		ip = ip[:colon]
	}
	return ip
}

// veridianRateLimitIdentity extrait une CLÉ d'identité best-effort pour le
// rate-limit, sans valider la signature (la vraie auth tourne plus loin). Ordre :
//
//  1. Bearer JWT → claim "user_id" (décodé du payload, NON vérifié). Clé
//     "user:<id>". Suffisant comme clé de bucketing ; pas une frontière de sécu.
//  2. HMAC app caller : header x-veridian-app (+ workspace via query/header).
//     Clé "app:<app>:<workspace>". (Note : le flux HMAC Hub est déjà exempté en
//     amont via X-Veridian-Hub-Signature ; ce cas couvre d'éventuels callers
//     HMAC futurs sans ce header précis.)
//
// Retourne "" si rien d'exploitable → seule la dimension IP s'applique.
func veridianRateLimitIdentity(r *http.Request) string {
	if uid := veridianUserIDFromBearer(r.Header.Get("Authorization")); uid != "" {
		return "user:" + uid
	}

	if app := strings.TrimSpace(r.Header.Get("x-veridian-app")); app != "" {
		ws := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if ws == "" {
			ws = strings.TrimSpace(r.Header.Get("X-Workspace-Id"))
		}
		return "app:" + app + ":" + ws
	}

	return ""
}

// veridianUserIDFromBearer décode (sans vérifier) le payload d'un JWT "Bearer
// xxx" et retourne son claim user_id. Best-effort : tout échec de parsing →
// "". On ne fait PAS confiance à cette valeur pour l'auth — uniquement comme
// clé de rate-limit (cf. doc du middleware).
func veridianUserIDFromBearer(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if authHeader == "" {
		return ""
	}
	parts := strings.Fields(authHeader)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}

	segments := strings.Split(parts[1], ".")
	if len(segments) != 3 {
		return ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		// Tolère un éventuel padding standard.
		payload, err = base64.URLEncoding.DecodeString(segments[1])
		if err != nil {
			return ""
		}
	}

	var claims struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.UserID)
}

// veridianRateLimitConfigString : helper de log lisible au boot (non utilisé en
// hot path).
func (c VeridianAPIRateLimitConfig) String() string {
	if !c.Enabled {
		return "disabled"
	}
	return fmt.Sprintf("enabled per_ip=%d/min per_identity=%d/min", c.PerIP, c.PerIdentity)
}
