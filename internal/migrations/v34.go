package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V34Migration ajoute les colonnes lifecycle a veridian_plan (CONTRAT-HUB
// sec. 5.7 + sec. 5.8) :
//
//   - restored_at         : timestamp du dernier Restore (audit trail)
//   - purge_eligible_at   : timestamp a partir duquel le tenant peut etre
//                           hard-deleted (NOW + 30j au soft-delete)
//   - last_touched_at     : timestamp du dernier Touch (anti-soft-delete par cron)
//   - lifecycle_reason    : derniere raison applique au lifecycle (audit GDPR)
//
// Toutes les colonnes sont nullables / sans default contraignant : ajout
// pur (Expand & Contract clean, pas de DROP, pas de NOT NULL sur table
// peuplee).
//
// Pas d'index ici : les requetes "tenants en attente de purge" sont rares
// (cron quotidien) et veridian_plan est petit (<10k rows aujourd'hui). Si
// besoin, ajouter un index partiel hors-TX dans une migration ulterieure.
type V34Migration struct{}

func (m *V34Migration) GetMajorVersion() float64 {
	return 34.0
}

func (m *V34Migration) HasSystemUpdate() bool {
	return true
}

func (m *V34Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V34Migration) ShouldRestartServer() bool {
	return false
}

func (m *V34Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS restored_at TIMESTAMP WITH TIME ZONE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.restored_at: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS purge_eligible_at TIMESTAMP WITH TIME ZONE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.purge_eligible_at: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS last_touched_at TIMESTAMP WITH TIME ZONE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.last_touched_at: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS lifecycle_reason TEXT
	`); err != nil {
		return fmt.Errorf("add veridian_plan.lifecycle_reason: %w", err)
	}
	return nil
}

func (m *V34Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V34Migration{})
}
