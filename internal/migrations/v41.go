package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V41Migration cree la table veridian_api_key_grace pour le grace period
// 5min apres rotate-api-key (CONTRAT-HUB §5.15).
//
// Lot K — ticket 2026-05-19-rotate-transfer-owner-endpoints.md.
//
// Schema :
//
//   api_key_user_id TEXT PRIMARY KEY  — id du user api_key marque pour
//                                       revocation (FK logique sur users.id)
//   workspace_id    TEXT NOT NULL     — workspace concerne (pour audit/cron)
//   revoke_at       TIMESTAMPTZ NOT NULL — moment ou le cron doit DELETE le user
//   reason          TEXT              — audit GDPR (passe par rotate handler)
//   created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
//
// Le cron veridian_api_key_grace_cleanup (1×/min) :
//   1. SELECT api_key_user_id FROM veridian_api_key_grace WHERE revoke_at <= NOW()
//   2. DELETE FROM users WHERE id IN (...)
//   3. DELETE FROM veridian_api_key_grace WHERE api_key_user_id IN (...)
//
// Pendant la grace period (typiquement 5min), les DEUX api_keys (ancienne et
// nouvelle) restent valides — comportement explicitement attendu par le contrat
// (zero downtime cote Hub). Apres expiration : ancienne key supprimee →
// authentification echoue (GetUserByID renvoie sql.ErrNoRows → 401).
//
// Pas d'index supplementaire : la table est petite (rows transientes 5min) et
// le cron scan complet est trivial. Pas de CREATE INDEX car CONCURRENTLY est
// interdit dans la transaction d'execution UpdateSystem (manager.executeMigration
// wrappe tout en BEGIN/COMMIT), et CREATE INDEX sans CONCURRENTLY lock la table
// (bloque par check-migration-safety.sh §12 Expand & Contract).
//
// Additive, idempotent (IF NOT EXISTS). Pas de DROP — back-compat avec V40.
type V41Migration struct{}

func (m *V41Migration) GetMajorVersion() float64 {
	return 41.0
}

func (m *V41Migration) HasSystemUpdate() bool {
	return true
}

func (m *V41Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V41Migration) ShouldRestartServer() bool {
	return false
}

func (m *V41Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS veridian_api_key_grace (
			api_key_user_id TEXT PRIMARY KEY,
			workspace_id    TEXT NOT NULL,
			revoke_at       TIMESTAMP WITH TIME ZONE NOT NULL,
			reason          TEXT,
			created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create veridian_api_key_grace: %w", err)
	}

	return nil
}

func (m *V41Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V41Migration{})
}
