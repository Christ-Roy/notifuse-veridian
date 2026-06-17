package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V54Migration RÉCONCILIE la colonne `message_history.bounce_type` qui était
// SUPPOSÉE exister mais n'a JAMAIS été déclarée nulle part (ni init.go, ni
// migration) — bug P0 dashboard 500 du 2026-06-17.
//
// CONTEXTE DU BUG (diagnostiqué en conditions réelles) :
//
//   - Le lot KPI bounce hard/soft (commit 5414a3d7) a ajouté à
//     internal/domain/analytics.go deux mesures `count_bounced_hard` /
//     `count_bounced_soft` filtrant sur `bounce_type ILIKE 'hard%'` / `'soft%'`.
//     Le commentaire du lot affirmait « la colonne existe depuis v8 ».
//   - C'était FAUX. La seule occurrence `bounce_type VARCHAR(100)` dans init.go
//     (ligne 246) appartient à la table `inbound_webhook_events`, PAS à
//     `message_history`. Le bloc CREATE TABLE message_history (init.go) va de
//     `updated_at` → `veridian_content_hash` → `veridian_sender_email`, SANS
//     `bounce_type`. AUCUNE migration n'a jamais fait
//     `ALTER TABLE message_history ADD COLUMN bounce_type`.
//   - Vérifié sur la DB staging réelle (2026-06-17) : la colonne est ABSENTE de
//     message_history sur TOUS les workspaces (dasherr873, coldtunnel, canary*).
//   - Conséquence : `POST /api/analytics.query` → 500
//     `pq: column "bounce_type" does not exist` → tout l'Email Metrics du
//     dashboard tombe (graphique + KPI). De plus le chemin d'ÉCRITURE du KPI
//     (message_history_postgre.go:SetStatusesIfNotSet, qui pose
//     `bounce_type = COALESCE(...)`) était lui aussi cassé sur tout workspace.
//
// FIX À LA RACINE (voie A, R0 — la colonne DOIT exister, l'analytics ET le write
// path la référencent déjà) : on ajoute la colonne via migration additive +
// init.go (cohérence nouveaux workspaces). C'est ce qui rend le KPI bounce
// hard/soft réellement fonctionnel (avant V54 il était mort/cassé) et débloque
// le dashboard sur tous les workspaces existants.
//
//   - colonne `bounce_type VARCHAR(100)` (nullable) : type IDENTIQUE à celui de
//     `inbound_webhook_events.bounce_type` (init.go:246) et à la déclaration
//     attendue. NULL pour tous les messages historiques — additif pur, aucune
//     réécriture de données. Alimentée à la classification du bounce avec les
//     littéraux "HardBounce"/"SoftBounce" (cf. inbound_webhook_event_service.go,
//     VeridianBounceTypeLabel). Pas d'index : les mesures analytics filtrent
//     déjà par `bounced_at IS NOT NULL` + fenêtre `created_at` (indexée) ; le
//     volume bouncé est faible, le `ILIKE` sur ce sous-ensemble est négligeable.
//
// Safety §12 (Expand & Contract) : ADD COLUMN nullable additif pur, idempotent
// (IF NOT EXISTS). Le tag Docker précédent (V53) tourne sur ce schéma sans
// problème (il ignore la colonne). PAS de CONCURRENTLY (pas d'index ici, et de
// toute façon les migrations Notifuse tournent en TX — cf. manager.go BeginTx —
// où CONCURRENTLY est interdit). Aucun fichier upstream patché : la colonne
// existait déjà côté init.go pour les NOUVEAUX workspaces après ce commit, et la
// migration la rattrape sur les workspaces EXISTANTS.
type V54Migration struct{}

func (m *V54Migration) GetMajorVersion() float64 {
	return 54.0
}

func (m *V54Migration) HasSystemUpdate() bool {
	return false
}

func (m *V54Migration) HasWorkspaceUpdate() bool {
	return true
}

func (m *V54Migration) ShouldRestartServer() bool {
	return false
}

func (m *V54Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V54Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	// Colonne bounce_type (nullable, additive). IF NOT EXISTS : idempotent,
	// rejouable sans erreur sur un workspace déjà migré ou créé après init.go
	// (où la colonne sera désormais présente dès le CREATE TABLE).
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE message_history
		ADD COLUMN IF NOT EXISTS bounce_type VARCHAR(100)
	`); err != nil {
		return fmt.Errorf("add message_history.bounce_type: %w", err)
	}

	return nil
}

func init() {
	Register(&V54Migration{})
}
