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

// paywallCache implemente un cache thread-safe avec TTL.
type paywallCache struct {
	m sync.Map // map[string]paywallCacheEntry
}

func (c *paywallCache) get(workspaceID string) (paywallCacheEntry, bool) {
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

func (c *paywallCache) set(workspaceID string, entry paywallCacheEntry) {
	c.m.Store(workspaceID, entry)
}

// NewVeridianPaywallMiddleware retourne un middleware applicable sur les
// endpoints d'envoi. Si planRepo est nil, le middleware est un passthrough
// (mode self-hosted sans veridian_plan).
func NewVeridianPaywallMiddleware(planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	cache := &paywallCache{}

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
// s'active (exact match). Les autres paths passent sans inspection.
var paywallProtectedPaths = map[string]struct{}{
	"/api/transactional.send":           {},
	"/api/broadcasts.create":            {},
	"/api/broadcasts.schedule":          {},
	"/api/broadcasts.sendToIndividual":  {},
}

// VeridianPaywallPathFilter est un wrapper applique au niveau Server.Handler
// global. Pour les paths dans paywallProtectedPaths, il delegue au middleware
// paywall ; pour les autres, il passe directement au handler suivant.
//
// Ca permet d'eviter de toucher aux RegisterRoutes des handlers upstream
// (transactional, broadcast) tout en garantissant que tous les envois
// passent par le paywall, peu importe l'ordre d'enregistrement des routes.
func VeridianPaywallPathFilter(planRepo domain.VeridianPlanRepository, log logger.Logger) func(http.Handler) http.Handler {
	paywall := NewVeridianPaywallMiddleware(planRepo, log)
	return func(next http.Handler) http.Handler {
		protected := paywall(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, isProtected := paywallProtectedPaths[r.URL.Path]; isProtected {
				protected.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
