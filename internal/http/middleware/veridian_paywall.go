package middleware

// === Veridian patch ===
// VeridianPaywallMiddleware bloque les envois pour les tenants suspended,
// deleted, ou ayant atteint leur quota mensuel. S'applique sur :
//   - /api/transactional.send
//   - /api/broadcasts.create
//   - /api/broadcasts.schedule
//
// Le middleware lit le body JSON pour extraire workspace_id (sans le
// consommer pour le handler suivant), regarde la table veridian_plan via
// le repo, et retourne 402 Payment Required si IsBlocked.
//
// Si le workspace_id n'a PAS de ligne veridian_plan (sql.ErrNoRows), on
// laisse passer : ce workspace n'est pas gere par Veridian (mode self-
// hosted ou workspace upstream cree avant migration).
//
// Cache en memoire 60s (sync.Map + TTL) pour eviter un round-trip DB par
// envoi. Le TTL court permet aux suspend/resume de prendre effet en moins
// d'une minute sans invalidation manuelle.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	// veridianPaywallCacheTTL est la duree de vie d'une entree de cache.
	// Trade-off : plus long = moins de requetes DB ; plus court = react
	// plus vite aux suspend/resume. 60s est un compromis confortable
	// pour un Hub qui peut tolerer une latence d'invalidation < 1 min.
	veridianPaywallCacheTTL = 60 * time.Second

	// veridianPaywallMaxBody limite le body a 4 MiB (le decode JSON ne
	// lit que workspace_id, mais on doit copier le body pour le rebuffer
	// au handler suivant). 4 MiB couvre des broadcasts avec attachments
	// modestes ; au-dela, le handler upstream a son propre LimitReader.
	veridianPaywallMaxBody = 4 << 20 // 4 MiB
)

// paywallCacheEntry est une ligne de cache.
type paywallCacheEntry struct {
	plan      *domain.VeridianPlan
	notFound  bool // workspace pas geree par Veridian (sql.ErrNoRows)
	expiresAt time.Time
}

// PaywallCache est un cache thread-safe avec TTL pour les decisions paywall.
// Exporte pour que des appelants externes (handler admin) puissent invalider
// une entree apres un suspend/resume/update-plan, sans attendre l'expiration
// naturelle (60s par defaut). Reduit drastiquement le wall-clock e2e des
// scenarios paywall en CI (gain ~5min par run).
type PaywallCache struct {
	m sync.Map // map[string]paywallCacheEntry
}

// NewPaywallCache cree un cache vide. Utiliser pour le wiring middleware
// + handler admin invalidate (un seul cache partage).
func NewPaywallCache() *PaywallCache {
	return &PaywallCache{}
}

func (c *PaywallCache) get(workspaceID string) (paywallCacheEntry, bool) {
	v, ok := c.m.Load(workspaceID)
	if !ok {
		return paywallCacheEntry{}, false
	}
	entry := v.(paywallCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.m.Delete(workspaceID)
		return paywallCacheEntry{}, false
	}
	return entry, true
}

func (c *PaywallCache) set(workspaceID string, entry paywallCacheEntry) {
	c.m.Store(workspaceID, entry)
}

// Invalidate supprime l'entree pour ce workspace_id. Idempotent : pas
// d'erreur si le workspace n'avait pas d'entree en cache. Le prochain
// passage paywall fera un fresh DB lookup.
func (c *PaywallCache) Invalidate(workspaceID string) {
	c.m.Delete(workspaceID)
}

// Clear supprime toutes les entrees du cache. Reserve aux cas exceptionnels
// (test cleanup, reload config). En prod, prefere Invalidate(workspaceID)
// sur l'evenement specifique pour eviter les recalculs de tous les tenants.
func (c *PaywallCache) Clear() {
	c.m.Range(func(key, _ any) bool {
		c.m.Delete(key)
		return true
	})
}

// NewVeridianPaywallMiddleware retourne un middleware applicable sur les
// endpoints d'envoi. Si planRepo est nil, le middleware est un passthrough
// (mode self-hosted sans veridian_plan). Cache local non partage : utiliser
// NewVeridianPaywallMiddlewareWithCache si on veut invalider depuis l'exterieur.
func NewVeridianPaywallMiddleware(planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return NewVeridianPaywallMiddlewareWithCache(NewPaywallCache(), planRepo, log)
}

// NewVeridianPaywallMiddlewareWithCache permet d'injecter un cache partage
// pour que le handler admin /api/veridian/admin/cache/invalidate puisse
// invalider une entree sans attendre l'expiration TTL.
func NewVeridianPaywallMiddlewareWithCache(cache *PaywallCache, planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if planRepo == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Le paywall ne s'applique qu'aux requetes qui portent un body
			// JSON contenant workspace_id. Pour les GET, OPTIONS, etc., on
			// laisse passer.
			if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			// Lire et rebufferer le body.
			body, err := io.ReadAll(io.LimitReader(r.Body, veridianPaywallMaxBody+1))
			if err != nil {
				writeJSONError(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > veridianPaywallMaxBody {
				writeJSONError(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			// Extraire workspace_id sans consommer ni invalider le body.
			// On parse uniquement le champ workspace_id pour minimiser le
			// cout (json.RawMessage ailleurs).
			var probe struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if err := json.Unmarshal(body, &probe); err != nil {
				// Body non-JSON ou JSON invalide : on laisse le handler
				// upstream gerer (il fera son propre 400).
				next.ServeHTTP(w, r)
				return
			}
			if probe.WorkspaceID == "" {
				// Pas de workspace_id dans le body, on laisse passer (le
				// handler upstream rejettera ou utilisera un autre champ).
				next.ServeHTTP(w, r)
				return
			}

			// Cache lookup.
			entry, hit := cache.get(probe.WorkspaceID)
			if !hit {
				plan, err := planRepo.Get(r.Context(), probe.WorkspaceID)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						entry = paywallCacheEntry{
							notFound:  true,
							expiresAt: time.Now().Add(veridianPaywallCacheTTL),
						}
						cache.set(probe.WorkspaceID, entry)
					} else {
						// Erreur DB transitoire : on log et laisse passer
						// (fail-open). L'alternative (fail-closed) bloquerait
						// tous les envois sur incident DB.
						if log != nil {
							log.WithFields(map[string]interface{}{
								"workspace_id": probe.WorkspaceID,
								"error":        err.Error(),
							}).Warn("veridian paywall: failed to read plan, allowing request")
						}
						next.ServeHTTP(w, r)
						return
					}
				} else {
					entry = paywallCacheEntry{
						plan:      plan,
						expiresAt: time.Now().Add(veridianPaywallCacheTTL),
					}
					cache.set(probe.WorkspaceID, entry)
				}
			}

			// Workspace pas gere par Veridian : passe.
			if entry.notFound {
				next.ServeHTTP(w, r)
				return
			}

			// Plan present : verifier le blocage.
			blocked, reason := entry.plan.IsBlocked()
			if blocked {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusPaymentRequired)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":         "Payment required: " + reason,
					"tenant_status": string(entry.plan.Status),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// paywallProtectedPaths est l'ensemble des paths sur lesquels le paywall
// status (suspended/deleted) s'active (exact match). Les autres paths
// passent sans inspection sauf s'ils sont dans une autre map specialisee.
var paywallProtectedPaths = map[string]struct{}{
	"/api/transactional.send":           {},
	"/api/broadcasts.create":            {},
	"/api/broadcasts.schedule":          {},
	"/api/broadcasts.sendToIndividual":  {},
}

// featureGatedPaths mappe un path API → la cle feature dans PlanLimits qui
// doit etre TRUE pour autoriser l'acces. Si la feature est false sur le plan
// du tenant, le middleware retourne 402 avec error_code=feature_not_in_plan.
//
// === PIVOT 2026-05-21 ===
// Vide intentionnellement : decision Robert "generosite maximale, A/B
// testing gratuit pour tous y compris Free" (cf. CLAUDE.md Notifuse
// §Vision pricing 2026-05-21). Le middleware feature gate reste en place
// au cas ou on re-gaterait une autre feature plus tard, mais aucun path
// n'est gate aujourd'hui. Pour reactivation : ajouter
// "/api/path" → "feature_key" et flipper le champ correspondant dans
// DefaultPlanLimits.
//
// Historique : lot 4a V37 avait gate les 2 endpoints A/B (getTestResults,
// selectWinner) — revert acte le 2026-05-21 suite au pivot generosite.
var featureGatedPaths = map[string]string{}

// checkFeatureAllowed retourne true si la feature est activee sur le plan,
// false sinon. Si plan est nil (tenant non-Veridian / self-hosted), on
// considere que toutes les features sont autorisees (pas de paywall actif).
func checkFeatureAllowed(plan *domain.VeridianPlan, feature string) bool {
	if plan == nil {
		return true
	}
	switch feature {
	case "ab_testing":
		return plan.FeatureABTesting
	case "branding_removed":
		return plan.FeatureBrandingRemoved
	case "white_label":
		return plan.FeatureWhiteLabel
	default:
		// Feature inconnue dans la map : fail-open (= laisse passer) pour
		// ne pas casser un endpoint dont le mapping aurait une typo. Mieux
		// vaut une feature non-gatee qu'un endpoint qui plante en prod.
		return true
	}
}

// VeridianPaywallPathFilter est un wrapper applique au niveau Server.Handler
// global. Pour les paths dans paywallProtectedPaths, il delegue au middleware
// paywall ; pour les autres, il passe directement au handler suivant.
//
// Ca permet d'eviter de toucher aux RegisterRoutes des handlers upstream
// (transactional, broadcast) tout en garantissant que tous les envois
// passent par le paywall, peu importe l'ordre d'enregistrement des routes.
func VeridianPaywallPathFilter(planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return VeridianPaywallPathFilterWithCache(NewPaywallCache(), planRepo, log)
}

// VeridianPaywallPathFilterWithCache permet d'injecter un cache partage avec
// le handler admin invalidate. A utiliser dans app.Start() en passant le
// meme *PaywallCache qui a ete fourni au VeridianHandler.
//
// V37 lot 4a : ajoute le routage vers le feature gate middleware pour les
// paths dans featureGatedPaths (A/B testing). Le path filter check d'abord
// les protected paths (suspend/delete), puis les feature gated paths, sinon
// passe direct.
func VeridianPaywallPathFilterWithCache(cache *PaywallCache, planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	paywall := NewVeridianPaywallMiddlewareWithCache(cache, planRepo, log)
	featureGate := NewVeridianFeatureGateMiddlewareWithCache(cache, planRepo, log)
	return func(next http.Handler) http.Handler {
		protected := paywall(next)
		gated := featureGate(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, isProtected := paywallProtectedPaths[r.URL.Path]; isProtected {
				protected.ServeHTTP(w, r)
				return
			}
			if _, isGated := featureGatedPaths[r.URL.Path]; isGated {
				gated.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewVeridianFeatureGateMiddlewareWithCache retourne un middleware qui bloque
// les endpoints feature-gated si la feature n'est pas active sur le plan du
// tenant (V37 lot 4a). Reutilise le PaywallCache existant pour eviter un
// round-trip DB par requete.
//
// Le mapping path → feature key vit dans featureGatedPaths (mutuel-exclusif
// avec paywallProtectedPaths). Si le tenant n'a pas de ligne veridian_plan
// (mode self-hosted), on laisse passer (fail-open).
//
// La detection de workspace_id reutilise la meme heuristique que le paywall
// (lecture body JSON + champ workspace_id), donc compatible avec tous les
// endpoints broadcast existants.
//
// Reponse 402 :
//
//	{
//	  "error": "feature_not_in_plan: ab_testing requires a Pro or higher plan",
//	  "error_code": "feature_not_in_plan",
//	  "feature": "ab_testing",
//	  "tenant_plan": "free"
//	}
func NewVeridianFeatureGateMiddlewareWithCache(cache *PaywallCache, planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if planRepo == nil {
				next.ServeHTTP(w, r)
				return
			}
			if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			feature, isGated := featureGatedPaths[r.URL.Path]
			if !isGated {
				// Ne devrait pas arriver vu le routage upstream, mais belt &
				// braces : si on est appele sur un path non-gated, passe.
				next.ServeHTTP(w, r)
				return
			}

			// Lire et rebufferer le body (meme pattern que paywall).
			body, err := io.ReadAll(io.LimitReader(r.Body, veridianPaywallMaxBody+1))
			if err != nil {
				writeJSONError(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > veridianPaywallMaxBody {
				writeJSONError(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			var probe struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if err := json.Unmarshal(body, &probe); err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if probe.WorkspaceID == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Cache lookup (partage avec le paywall — meme cache, meme TTL).
			entry, hit := cache.get(probe.WorkspaceID)
			if !hit {
				plan, err := planRepo.Get(r.Context(), probe.WorkspaceID)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						entry = paywallCacheEntry{
							notFound:  true,
							expiresAt: time.Now().Add(veridianPaywallCacheTTL),
						}
						cache.set(probe.WorkspaceID, entry)
					} else {
						// Fail-open sur erreur DB transitoire — meme decision que paywall.
						if log != nil {
							log.WithFields(map[string]interface{}{
								"workspace_id": probe.WorkspaceID,
								"feature":      feature,
								"error":        err.Error(),
							}).Warn("veridian feature gate: failed to read plan, allowing request")
						}
						next.ServeHTTP(w, r)
						return
					}
				} else {
					entry = paywallCacheEntry{
						plan:      plan,
						expiresAt: time.Now().Add(veridianPaywallCacheTTL),
					}
					cache.set(probe.WorkspaceID, entry)
				}
			}

			// Workspace pas gere par Veridian : passe (fail-open).
			if entry.notFound {
				next.ServeHTTP(w, r)
				return
			}

			// Verifier l'activation feature.
			if !checkFeatureAllowed(entry.plan, feature) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusPaymentRequired)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":       "feature_not_in_plan: " + feature + " requires a Pro or higher plan",
					"error_code":  "feature_not_in_plan",
					"feature":     feature,
					"tenant_plan": entry.plan.Plan,
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
