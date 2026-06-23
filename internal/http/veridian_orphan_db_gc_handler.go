package http

// === Veridian patch — 2026-06-18 — endpoint GC bases orphelines (staging-only) ===
//
// POST /api/veridian/admin/gc-orphan-workspace-dbs. Auth HMAC Hub.
// STRICTEMENT staging : 503 hors staging (même garde-fou que cold-simulate /
// test-tenants-stats). Voir todo/2026-06-17-orphan-workspaces-staging-db-starvation.md.
//
// DROP les bases physiques notifuse_ws_* qui n'ont plus de record `workspaces`
// (orphelines : record wipé, DROP upstream raté faute de FORCE). SÉQUENTIEL,
// FORCE, exclut les préfixes de safety (canary + clients réels).
//
// Body (tous optionnels) :
//   { "dry_run": bool, "safety_prefixes": [...], "max_drops": int }
// dry_run=true → liste les droppables SANS rien DROP (inspection avant action).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Notifuse/notifuse/internal/service"
)

// VeridianOrphanDBGCRunner est la capacité consommée par le handler. Implémentée
// par *service.veridianService (méthode VeridianGCOrphanWorkspaceDBs). Interface
// étroite → testable + découplée du service concret. Exportée pour que app.go
// puisse type-asserter le service concret (non-exporté) dessus.
type VeridianOrphanDBGCRunner interface {
	VeridianGCOrphanWorkspaceDBs(ctx context.Context, input service.VeridianOrphanDBGCInput) (*service.VeridianOrphanDBGCResponse, error)
}

// veridianOrphanDBGCDeps regroupe les deps de l'endpoint. nil par défaut
// (self-hosted / prod) → 503.
type veridianOrphanDBGCDeps struct {
	runner      VeridianOrphanDBGCRunner
	environment string
}

// veridianOrphanDBGCStagingEnv : seule valeur d'environment qui active l'endpoint.
const veridianOrphanDBGCStagingEnv = "staging"

// SetOrphanDBGC injecte les deps de l'endpoint GC. Optionnel : non appelé ou
// environment != staging → 503.
func (h *VeridianHandler) SetOrphanDBGC(runner VeridianOrphanDBGCRunner, environment string) {
	h.orphanDBGC = &veridianOrphanDBGCDeps{
		runner:      runner,
		environment: environment,
	}
}

// handleGCOrphanWorkspaceDBs déclenche un run de GC. Staging-only.
func (h *VeridianHandler) handleGCOrphanWorkspaceDBs(w http.ResponseWriter, r *http.Request) {
	deps := h.orphanDBGC
	if deps == nil || deps.runner == nil || deps.environment != veridianOrphanDBGCStagingEnv {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable,
			"gc-orphan-workspace-dbs disabled (staging-only endpoint)",
			http.StatusServiceUnavailable, nil)
		return
	}

	// Body optionnel : un POST/GET sans body est valide (defaults). Un body vide
	// → io.EOF qu'on ignore ; tout autre JSON invalide → 400.
	var input service.VeridianOrphanDBGCInput
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
			return
		}
	}

	gcResp, gcErr := deps.runner.VeridianGCOrphanWorkspaceDBs(r.Context(), input)
	if gcErr != nil {
		h.logError("gc_orphan_workspace_dbs", gcErr, map[string]interface{}{
			"dry_run": input.DryRun,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, gcErr.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, gcResp)
}
