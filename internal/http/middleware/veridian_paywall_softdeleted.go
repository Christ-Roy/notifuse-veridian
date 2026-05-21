package middleware

// === Veridian patch — Lot J 2026-05-21 ===
// Mode dégradé soft-deleted : quand un tenant a `deleted_at != NULL` (Hub
// a déclenché SoftDelete via /api/tenants/{id}/soft-delete), Notifuse passe
// en MODE DÉGRADÉ au lieu d'un mur béton 402 :
//
//   - Routes lecture (GET/HEAD/OPTIONS) : on capture la response du
//     handler upstream via veridianResponseCapturer, on tente de
//     désérialiser le JSON et d'obfusquer toutes les string values
//     (33% en clair + reste `•••`). Champs SENSITIVE_FIELDS toujours
//     full obfusqués.
//   - Routes écriture (POST/PUT/PATCH/DELETE) : 402 avec body
//     standardisé (cf. writeSoftDeletedResponse).
//
// Le middleware s'applique GLOBAL (sur tout l'arbre HTTP) MAIS exempte :
//   - `/api/veridian/*` : routes admin Hub (le Hub doit pouvoir restore)
//   - `/api/tenants/*`  : routes HMAC Hub (le Hub manage le tenant)
//   - `/api/health`, `/api/version`, `/api/auth/*` : routes système
//
// On peut identifier le workspace_id via :
//   - query string (`?workspace_id=ws-X`) : standard pour les GET reads
//   - body JSON (`{"workspace_id":"ws-X"}`) : standard pour les POST writes
//
// Si workspace_id absent ou plan non-Veridian (sql.ErrNoRows) : passe-
// through (le handler upstream gère sa propre auth et son propre 401).
//
// Cohabitation avec PaywallPathFilter (envoi) et FeatureGate :
//
//   handler =
//     VeridianSoftDeletedFilterWithCache(  // <- outer (s'applique partout)
//       VeridianPaywallPathFilterWithCache(  // <- inner (4 paths d'envoi)
//         next))
//
// Le soft-deleted prime : si tenant deleted, la réponse 402 standardisée
// est renvoyée AVANT que le path filter ait l'occasion de voir le
// suspended/HubSyncDead.
//
// Le helper writeSoftDeletedResponse est aussi appelé par
// NewVeridianPaywallMiddlewareWithCache (branche IsBlocked.DeletedAt) pour
// que les 4 paths d'envoi (transactional.send, broadcasts.*) renvoient le
// même body que le middleware global — cohérence cross-route.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// veridianSoftDeletedExemptPrefixes liste les préfixes de path exemptés
// du mode dégradé soft-deleted. Le Hub doit pouvoir restore un tenant
// même si deleted, donc /api/veridian/* (admin Hub) et /api/tenants/*
// (HMAC Hub) passent à travers sans check.
//
// /api/health et /api/version sont exemptés pour rester observables
// même si tous les tenants sont deleted (smoke tests CI).
var veridianSoftDeletedExemptPrefixes = []string{
	"/api/veridian/",
	"/api/tenants/",
	"/api/health",
	"/api/version",
	"/api/auth/",
}

// isSoftDeletedExemptPath retourne true si le path donné est exempté du
// middleware soft-deleted (admin Hub, HMAC tenants, health/version).
func isSoftDeletedExemptPath(path string) bool {
	for _, prefix := range veridianSoftDeletedExemptPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// extractWorkspaceIDForSoftDelete tente d'extraire le workspace_id depuis
// la requête, en priorité depuis la query string (GET reads), sinon depuis
// le body JSON (POST writes). Retourne ("", false) si non trouvé.
//
// Pour les requêtes avec body, le body est lu puis rebufferé pour que le
// handler upstream puisse le relire. Limité à 4 MiB comme le middleware
// paywall (veridianPaywallMaxBody).
func extractWorkspaceIDForSoftDelete(r *http.Request) (string, bool) {
	// Query string : priorité (cas reads)
	if wid := r.URL.Query().Get("workspace_id"); wid != "" {
		return wid, true
	}
	// Body : seulement si présent et méthode compatible
	if r.Body == nil {
		return "", false
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		// OK
	default:
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, veridianPaywallMaxBody+1))
	if err != nil {
		return "", false
	}
	if len(body) > veridianPaywallMaxBody {
		// Trop gros : on rebuffer quand même pour ne pas casser la requête,
		// le handler upstream gérera son propre 413.
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		return "", false
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	var probe struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", false
	}
	if probe.WorkspaceID == "" {
		return "", false
	}
	return probe.WorkspaceID, true
}

// writeSoftDeletedResponse émet la réponse 402 standardisée pour un tenant
// soft-deleted, avec le body tenant_soft_deleted + restore_url + dates.
// Cf. CONTRAT-HUB §5.9.
//
// Aussi appelé par NewVeridianPaywallMiddlewareWithCache (branche
// IsBlocked.DeletedAt) pour cohérence cross-route.
func writeSoftDeletedResponse(w http.ResponseWriter, plan *domain.VeridianPlan, workspaceID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)

	body := map[string]interface{}{
		"error":       "tenant_soft_deleted",
		"error_code":  "tenant_soft_deleted",
		"restore_url": veridianHubURL + "/dashboard?action=restore&tenant=" + workspaceID,
	}
	if plan.DeletedAt != nil {
		body["deleted_at"] = plan.DeletedAt.Format(time.RFC3339)
	}
	if plan.PurgeEligibleAt != nil {
		body["purge_eligible_at"] = plan.PurgeEligibleAt.Format(time.RFC3339)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// isSoftDeletedWrite retourne true si la méthode est POST/PUT/PATCH/DELETE
// (= mutation). Les autres (GET/HEAD/OPTIONS) sont des reads → obfusqués.
func isSoftDeletedWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// NewVeridianSoftDeletedMiddleware retourne le middleware qui implémente
// le mode dégradé soft-deleted. Cf. doc en tête de fichier.
//
// Si planRepo est nil, le middleware est un passthrough (mode self-hosted
// sans veridian_plan).
//
// Cache local non-partagé : utiliser NewVeridianSoftDeletedMiddlewareWithCache
// pour partager le cache avec le paywall middleware + l'admin invalidate.
func NewVeridianSoftDeletedMiddleware(planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return NewVeridianSoftDeletedMiddlewareWithCache(NewPaywallCache(), planRepo, log)
}

// NewVeridianSoftDeletedMiddlewareWithCache : variante avec cache partagé.
// À utiliser dans app.Start() avec le même *PaywallCache que le paywall +
// le handler admin invalidate.
func NewVeridianSoftDeletedMiddlewareWithCache(cache *PaywallCache, planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if planRepo == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Exempts : admin Hub, HMAC tenants, health/version, auth.
			if isSoftDeletedExemptPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Extraire workspace_id (query string OU body JSON).
			workspaceID, ok := extractWorkspaceIDForSoftDelete(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			// Lookup plan (avec cache 60s).
			entry, hit := cache.get(workspaceID)
			if !hit {
				plan, err := planRepo.Get(r.Context(), workspaceID)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						entry = paywallCacheEntry{
							notFound:  true,
							expiresAt: time.Now().Add(veridianPaywallCacheTTL),
						}
						cache.set(workspaceID, entry)
					} else {
						// Fail-open sur erreur DB transitoire — même décision
						// que le paywall middleware. Mieux vaut servir des
						// données en clair que tout casser sur incident DB.
						if log != nil {
							log.WithFields(map[string]interface{}{
								"workspace_id": workspaceID,
								"error":        err.Error(),
							}).Warn("veridian soft-deleted: failed to read plan, allowing request")
						}
						next.ServeHTTP(w, r)
						return
					}
				} else {
					entry = paywallCacheEntry{
						plan:      plan,
						expiresAt: time.Now().Add(veridianPaywallCacheTTL),
					}
					cache.set(workspaceID, entry)
				}
			}

			// Workspace non géré par Veridian : passe.
			if entry.notFound || entry.plan == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Tenant pas soft-deleted : passe (le paywall middleware
			// downstream peut encore gérer suspended/HubSyncDead).
			if entry.plan.DeletedAt == nil {
				next.ServeHTTP(w, r)
				return
			}

			// === Tenant soft-deleted ===

			// Writes : 402 standard.
			if isSoftDeletedWrite(r.Method) {
				writeSoftDeletedResponse(w, entry.plan, workspaceID)
				return
			}

			// Reads : on laisse le handler répondre, on capture la response,
			// on tente d'obfusquer le JSON et on flush.
			capturer := newVeridianResponseCapturer()
			next.ServeHTTP(capturer, r)

			// Si non-200 OK : passe-through (laisse l'erreur upstream).
			if capturer.statusCode < 200 || capturer.statusCode >= 300 {
				capturer.flushTo(w, nil)
				return
			}

			// Tentative obfuscation JSON.
			obf, isJSON := veridianObfuscateJSONBody(capturer.body.Bytes())
			if !isJSON {
				// Non-JSON (binaire, CSV, etc.) : passe-through avec log warn.
				// Les reads non-JSON sont rares (la majorité des endpoints
				// Notifuse retournent du JSON). Si ça arrive régulièrement
				// sur une route, c'est un signal à investiguer.
				if log != nil {
					log.WithFields(map[string]interface{}{
						"workspace_id": workspaceID,
						"path":         r.URL.Path,
						"content_type": capturer.header.Get("Content-Type"),
					}).Warn("veridian soft-deleted: non-JSON response, passing through unobfuscated")
				}
				capturer.flushTo(w, nil)
				return
			}

			// Headers spec : marqueurs UI console pour afficher le bandeau.
			capturer.header.Set("X-Tenant-Soft-Deleted", "true")
			if entry.plan.DeletedAt != nil {
				capturer.header.Set("X-Tenant-Deleted-At", entry.plan.DeletedAt.Format(time.RFC3339))
			}
			if entry.plan.PurgeEligibleAt != nil {
				capturer.header.Set("X-Tenant-Purge-At", entry.plan.PurgeEligibleAt.Format(time.RFC3339))
			}

			capturer.flushTo(w, obf)
		})
	}
}

// VeridianSoftDeletedFilter wrappe le middleware soft-deleted avec le
// même pattern de path filter que VeridianPaywallPathFilter. À utiliser
// au niveau app.Start() pour appliquer GLOBAL avant le paywall path
// filter.
//
// Variante avec cache partagé : VeridianSoftDeletedFilterWithCache.
func VeridianSoftDeletedFilter(planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return NewVeridianSoftDeletedMiddleware(planRepo, log)
}

// VeridianSoftDeletedFilterWithCache : variante avec cache partagé
// (recommandé pour le wiring app.Start).
func VeridianSoftDeletedFilterWithCache(cache *PaywallCache, planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	return NewVeridianSoftDeletedMiddlewareWithCache(cache, planRepo, log)
}
