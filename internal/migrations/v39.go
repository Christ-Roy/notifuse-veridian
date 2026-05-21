package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V39Migration ajoute la colonne last_hub_sync_at sur veridian_plan.
//
// Colonnes (additive, IF NOT EXISTS, cf. Constitution sec.12 Expand & Contract) :
//
//   - last_hub_sync_at  TIMESTAMP WITH TIME ZONE
//     Timestamp du dernier push Hub→Notifuse ayant abouti. Mis à jour
//     atomiquement par TouchHubSync à chaque mutation Hub (Provision,
//     UpdatePlan, Suspend, Resume, SoftDelete, Restore, Touch,
//     AttachOwner, AttachMember, GrantUnlimited). NULL = jamais synchronisé
//     (tenants antérieurs à V39 — backfillé par updated_at).
//
//     Signal de fraîcheur du lien Hub→Notifuse :
//     < 24h  → HubSyncFresh   (mode normal)
//     24-72h → HubSyncStale   (grace optimistic, log warn, continue à servir)
//     > 72h  → HubSyncDead    (dégradation paywall : writes bloqués 503)
//
//     Cf. domaine domain.EvaluateHubSyncStatus + middleware veridian_paywall.go.
//
// Backfill : last_hub_sync_at = updated_at pour les tenants existants.
// Approximation safe (updated_at est la dernière écriture connue, sous-estime
// légèrement pour les tenants jamais modifiés post-provision, mais le Hub
// envoie déjà des Touch via cron 1×/h et le tenant aura son timestamp frais
// très rapidement). Idempotent (WHERE last_hub_sync_at IS NULL).
//
// Pas d'index : veridian_plan est petit (<10k rows), lookup par
// workspace_id (PRIMARY KEY).
type V39Migration struct{}

func (m *V39Migration) GetMajorVersion() float64 {
	return 39.0
}

func (m *V39Migration) HasSystemUpdate() bool {
	return true
}

func (m *V39Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V39Migration) ShouldRestartServer() bool {
	return false
}

func (m *V39Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS last_hub_sync_at TIMESTAMP WITH TIME ZONE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.last_hub_sync_at: %w", err)
	}

	// Backfill : initialiser last_hub_sync_at = updated_at pour les tenants
	// existants dont le champ est encore NULL. Idempotent (WHERE IS NULL).
	// updated_at étant TIMESTAMP WITHOUT TIME ZONE legacy, on cast en TIMESTAMPTZ
	// pour éviter toute ambiguïté TZ (Postgres le fait implicitement mais
	// l'expliciter est plus clair).
	if _, err := db.ExecContext(ctx, `
		UPDATE veridian_plan
		SET last_hub_sync_at = updated_at AT TIME ZONE 'UTC'
		WHERE last_hub_sync_at IS NULL
	`); err != nil {
		return fmt.Errorf("backfill veridian_plan last_hub_sync_at: %w", err)
	}

	return nil
}

func (m *V39Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V39Migration{})
}
