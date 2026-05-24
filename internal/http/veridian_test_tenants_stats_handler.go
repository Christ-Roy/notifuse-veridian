package http

// === Veridian patch — 2026-05-24 ===
// Handler pour GET /api/veridian/admin/test-tenants-stats.
//
// Expose l'etat du cron VeridianTestTenantsCleanupService :
//   - total_test_tenants : workspaces matchant prefix `tst*`
//   - orphans_older_than_1h : ceux dont CreatedAt < now-1h (candidats wipe)
//   - last_auto_cleanup_at : timestamp UTC du dernier tick (zero si jamais)
//   - last_cleanup_wiped_count : nb wipes du dernier tick
//   - enabled : false si pas staging (preuve garde-fou prod)
//
// Auth : HMAC Hub (mux wrapper). Pas d'idempotency — read-only.
//
// Use case :
//   - Robert curl pour verifier que le cron tourne en staging
//   - Smoke test post-deploy : verifier enabled=true en staging et false en prod
//   - Diagnostic saturation pool : si orphans_older_than_1h croit sans baisse
//     entre 2 ticks, le cron est bloque ou wipe foire silencieusement
//
// Si le cron n'a pas ete branche (cas: l'app boot sans service initialise),
// retourne 503 — pas de "données vides muettes" ambigues.
//
// Cf. todo/2026-05-24-staging-db-pool-orphan-cleanup-auto.md.

import (
	"context"
	"net/http"

	"github.com/Notifuse/notifuse/internal/service"
)

// TestTenantsCleanupStatsProvider est l'interface minimale consommee par
// le handler. Permet aux tests d'injecter un fake sans demarrer un vrai cron.
type TestTenantsCleanupStatsProvider interface {
	Stats(ctx context.Context) (*service.TestTenantsCleanupStats, error)
}

// SetTestTenantsCleanup injecte le service cron pour l'endpoint stats.
// Optionnel : si nil, /api/veridian/admin/test-tenants-stats retourne 503.
func (h *VeridianHandler) SetTestTenantsCleanup(p TestTenantsCleanupStatsProvider) {
	h.testTenantsCleanup = p
}

// handleTestTenantsStats renvoie l'etat courant du cron cleanup + une mesure
// live de orphans. Auth HMAC en amont (route enregistree dans hmac wrapper).
//
// Reponse 200 : TestTenantsCleanupStats.
// Reponse 500 : si list workspaces fail (DB down). Stats partielles renvoyees
// quand meme en body pour faciliter le debug, mais avec error JSON wrapper.
// Reponse 503 : si le cron n'a pas ete injecte (mode self-hosted / boot partiel).
func (h *VeridianHandler) handleTestTenantsStats(w http.ResponseWriter, r *http.Request) {
	if h.testTenantsCleanup == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable,
			"test tenants cleanup not initialized (self-hosted mode or boot partial)",
			http.StatusServiceUnavailable, nil)
		return
	}
	stats, err := h.testTenantsCleanup.Stats(r.Context())
	if err != nil {
		// Best-effort : log mais on retourne quand meme stats partielles si non-nil.
		h.logError("test_tenants_stats", err, nil)
		if stats == nil {
			WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
			return
		}
		// Stats partielles : on emet 500 mais avec body pour debug.
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, map[string]interface{}{
			"partial_stats": stats,
		})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
