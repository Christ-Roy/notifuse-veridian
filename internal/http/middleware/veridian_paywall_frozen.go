package middleware

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
//
// VeridianFrozenMemberFilter applique le mode degrade per-user a partir de la
// table veridian_frozen_members (migration V47) :
//
//   - Routes lecture (GET/HEAD/OPTIONS) : reponse obfusquee §5.9 (reutilise
//     le pattern existant veridianResponseCapturer + veridianObfuscateJSONBody).
//   - Routes ecriture (POST/PUT/PATCH/DELETE) : 402 user_frozen avec body
//     {error, code, unfreeze_url}.
//
// Le middleware se distingue de VeridianSoftDeletedFilter (tenant-level) par :
//
//   - resolution du user_id depuis le JWT (le freeze est per-(workspace, user))
//   - skip si pas de JWT (routes publiques + tenant routes HMAC ne sont pas
//     concernees par le freeze user-side)
//   - lookup repo.IsFrozen avec cache 60s partage avec le paywall
//
// Exempts (paths inutiles a checker) :
//   - /api/veridian/*, /api/tenants/*  : routes admin Hub (HMAC, pas un user)
//   - /api/health, /api/version, /api/auth/*  : routes systeme
//
// Le freeze N'EST PAS irreversible cote app : seul un POST unfreeze-member
// du Hub peut le lever. Pas de cron cleanup app-side (cf. ticket §"Garde-fou
// anti-flap").

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
)

// veridianFrozenExemptPrefixes : paths qui n'ont aucun sens a checker pour
// freeze (admin Hub, health, auth, etc.). Aligne sur veridianSoftDeletedExemptPrefixes.
var veridianFrozenExemptPrefixes = []string{
	"/api/veridian/",
	"/api/tenants/",
	"/api/health",
	"/api/version",
	"/api/auth/",
}

// isFrozenExemptPath retourne true si le path donne est exempte du middleware
// freeze (admin Hub, HMAC tenants, health/version).
func isFrozenExemptPath(path string) bool {
	for _, prefix := range veridianFrozenExemptPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// frozenCacheEntry est une ligne de cache pour le freeze lookup.
// Cle composite : workspace_id + ":" + user_id.
type frozenCacheEntry struct {
	frozen    bool
	reason    domain.FreezeReason
	expiresAt time.Time
}

// FrozenMemberCache est un cache thread-safe avec TTL (60s, meme valeur que le
// paywall cache). Decouple du PaywallCache car la clef est composite
// (workspace_id, user_id) — un PaywallCache existe deja par workspace_id seul.
type FrozenMemberCache struct {
	m sync.Map // map[string]frozenCacheEntry
}

// NewFrozenMemberCache cree un cache vide.
func NewFrozenMemberCache() *FrozenMemberCache {
	return &FrozenMemberCache{}
}

func (c *FrozenMemberCache) key(workspaceID, userID string) string {
	return workspaceID + ":" + userID
}

func (c *FrozenMemberCache) get(workspaceID, userID string) (frozenCacheEntry, bool) {
	v, ok := c.m.Load(c.key(workspaceID, userID))
	if !ok {
		return frozenCacheEntry{}, false
	}
	entry := v.(frozenCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.m.Delete(c.key(workspaceID, userID))
		return frozenCacheEntry{}, false
	}
	return entry, true
}

func (c *FrozenMemberCache) set(workspaceID, userID string, entry frozenCacheEntry) {
	c.m.Store(c.key(workspaceID, userID), entry)
}

// Invalidate supprime l'entree pour ce (workspace, user). Appele apres
// freeze/unfreeze cote handler pour ne pas attendre l'expiration TTL.
// Idempotent.
func (c *FrozenMemberCache) Invalidate(workspaceID, userID string) {
	c.m.Delete(c.key(workspaceID, userID))
}

// Clear purge tout le cache. Reserve aux tests.
func (c *FrozenMemberCache) Clear() {
	c.m.Range(func(key, _ any) bool {
		c.m.Delete(key)
		return true
	})
}

// extractUserIDFromJWT lit l'Authorization: Bearer <jwt>, parse le token avec
// getJWTSecret(), et retourne le user_id depuis les claims. Si pas de header,
// JWT invalide, ou secret unavailable : ("", false) — le middleware passe-
// through (l'auth middleware downstream renverra 401 si la route exige auth).
func extractUserIDFromJWT(r *http.Request, getJWTSecret func() ([]byte, error)) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", false
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return "", false
	}
	secret, err := getJWTSecret()
	if err != nil || len(secret) == 0 {
		return "", false
	}

	// Parse minimal : on extrait juste le claim user_id sans valider le
	// type/session_id (le auth middleware downstream le fera si necessaire).
	// On utilise jwt.MapClaims pour eviter une dependance circulaire sur
	// service.UserClaims (import middleware → service deja present mais on
	// reste leger ici).
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(parts[1], &claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if err != nil || token == nil || !token.Valid {
		return "", false
	}

	// Le claim user_id est conventionnel dans service.UserClaims (champ
	// UserID json:"user_id"). On lit directement la map.
	userID, ok := claims["user_id"].(string)
	if !ok || userID == "" {
		return "", false
	}
	return userID, true
}

// writeFrozenResponse emet la reponse 402 standardisee pour un user frozen.
// Format aligne sur les autres 402 paywall :
//
//	{
//	  "error":        "user_frozen",
//	  "code":         "user_frozen",
//	  "message":      "Your access is temporarily restricted. Contact your workspace admin.",
//	  "unfreeze_url": "https://app.veridian.site/dashboard?action=unfreeze&tenant=<id>"
//	}
func writeFrozenResponse(w http.ResponseWriter, workspaceID string, reason domain.FreezeReason) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	body := map[string]interface{}{
		"error":        "user_frozen",
		"code":         "user_frozen",
		"message":      "Your access to this workspace is temporarily restricted. Contact your workspace admin.",
		"unfreeze_url": veridianHubURL + "/dashboard?action=unfreeze&tenant=" + workspaceID,
	}
	if reason != "" {
		body["reason"] = string(reason)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// isFrozenWrite retourne true si la methode est POST/PUT/PATCH/DELETE
// (= mutation). GET/HEAD/OPTIONS = reads → obfusques.
func isFrozenWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// NewVeridianFrozenMemberMiddleware retourne le middleware qui bloque/obfusque
// les requetes des users frozen. Si frozenRepo est nil, le middleware est un
// passthrough (mode self-hosted ou freeze pas configure).
//
// Cache local non-partage : utiliser NewVeridianFrozenMemberMiddlewareWithCache
// pour partager le cache avec le handler freeze/unfreeze qui doit invalider.
func NewVeridianFrozenMemberMiddleware(
	frozenRepo domain.VeridianFrozenMemberRepository,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) func(http.Handler) http.Handler {
	return NewVeridianFrozenMemberMiddlewareWithCache(NewFrozenMemberCache(), frozenRepo, getJWTSecret, log)
}

// NewVeridianFrozenMemberMiddlewareWithCache : variante avec cache partage.
// A utiliser dans app.Start() pour permettre au handler freeze/unfreeze
// d'invalider une entree apres mutation (sans attendre TTL 60s).
func NewVeridianFrozenMemberMiddlewareWithCache(
	cache *FrozenMemberCache,
	frozenRepo domain.VeridianFrozenMemberRepository,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Self-hosted ou pas de support freeze : passe.
			if frozenRepo == nil || getJWTSecret == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Exempts : admin Hub, HMAC tenants, health/version, auth.
			if isFrozenExemptPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Resoudre user_id depuis le JWT. Si pas de JWT (route publique
			// ou client non authentifie) : passe — le auth middleware downstream
			// se chargera de rejeter 401 si la route exige auth.
			userID, ok := extractUserIDFromJWT(r, getJWTSecret)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			// Resoudre workspace_id (query string OU body JSON).
			workspaceID, ok := extractWorkspaceIDForSoftDelete(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			// Cache lookup.
			entry, hit := cache.get(workspaceID, userID)
			if !hit {
				frozen, reason, err := frozenRepo.IsFrozen(r.Context(), workspaceID, userID)
				if err != nil {
					// Fail-open sur erreur DB transitoire — meme decision que les
					// autres paywall middlewares. Mieux servir une requete que tout
					// casser sur incident DB.
					if log != nil {
						log.WithFields(map[string]interface{}{
							"workspace_id": workspaceID,
							"user_id":      userID,
							"error":        err.Error(),
						}).Warn("veridian frozen: failed to lookup IsFrozen, allowing request")
					}
					next.ServeHTTP(w, r)
					return
				}
				entry = frozenCacheEntry{
					frozen:    frozen,
					reason:    reason,
					expiresAt: time.Now().Add(veridianPaywallCacheTTL),
				}
				cache.set(workspaceID, userID, entry)
			}

			// User pas frozen : passe.
			if !entry.frozen {
				next.ServeHTTP(w, r)
				return
			}

			// === User frozen ===

			// Writes : 402 standard.
			if isFrozenWrite(r.Method) {
				writeFrozenResponse(w, workspaceID, entry.reason)
				return
			}

			// Reads : on laisse le handler repondre, on capture la response,
			// on tente d'obfusquer le JSON et on flush. Reutilise le pattern
			// veridianResponseCapturer + veridianObfuscateJSONBody existant
			// (cf. veridian_paywall_softdeleted.go).
			capturer := newVeridianResponseCapturer()
			next.ServeHTTP(capturer, r)

			// Non-2xx : passe-through (laisse l'erreur upstream).
			if capturer.statusCode < 200 || capturer.statusCode >= 300 {
				capturer.flushTo(w, nil)
				return
			}

			obf, isJSON := veridianObfuscateJSONBody(capturer.body.Bytes())
			if !isJSON {
				if log != nil {
					log.WithFields(map[string]interface{}{
						"workspace_id": workspaceID,
						"user_id":      userID,
						"path":         r.URL.Path,
						"content_type": capturer.header.Get("Content-Type"),
					}).Warn("veridian frozen: non-JSON response, passing through unobfuscated")
				}
				capturer.flushTo(w, nil)
				return
			}

			// Headers spec : marqueurs UI console pour afficher la banniere.
			capturer.header.Set("X-User-Frozen", "true")
			if entry.reason != "" {
				capturer.header.Set("X-User-Frozen-Reason", string(entry.reason))
			}

			capturer.flushTo(w, obf)
		})
	}
}

// VeridianFrozenMemberFilter wrappe le middleware avec le meme pattern que
// VeridianSoftDeletedFilter. A utiliser au niveau app.Start() pour appliquer
// global.
func VeridianFrozenMemberFilter(
	frozenRepo domain.VeridianFrozenMemberRepository,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) func(http.Handler) http.Handler {
	return NewVeridianFrozenMemberMiddleware(frozenRepo, getJWTSecret, log)
}

// VeridianFrozenMemberFilterWithCache : variante avec cache partage
// (recommande pour wiring app.Start). Permet au handler freeze/unfreeze
// d'invalider une entree apres mutation.
func VeridianFrozenMemberFilterWithCache(
	cache *FrozenMemberCache,
	frozenRepo domain.VeridianFrozenMemberRepository,
	getJWTSecret func() ([]byte, error),
	log logger.Logger,
) func(http.Handler) http.Handler {
	return NewVeridianFrozenMemberMiddlewareWithCache(cache, frozenRepo, getJWTSecret, log)
}

// Compile-time guard : si jwt v5 enleve MapClaims un jour, on s'en rend
// compte au build (utile pour les refacteurs futurs).
var _ jwt.Claims = (*jwt.MapClaims)(nil)

// ensure context package is imported even if not directly used here (helper
// future-proofing — le compilateur peut le retirer s'il n'est pas utilise).
var _ = context.Background
