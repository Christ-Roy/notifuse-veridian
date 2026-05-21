package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V40Migration ajoute la colonne quota_exceeded_emitted_at_month sur veridian_plan.
//
// Lot I — Webhooks lifecycle (ticket todo/2026-05-19-webhooks-manquants.md).
// Support de l'idempotence MENSUELLE du webhook tenant.quota_exceeded : le Hub
// reçoit 1 et 1 seul event par tenant par mois calendaire, même si le tenant
// continue d'envoyer après franchissement.
//
// Colonne (additive, IF NOT EXISTS, cf. Constitution sec.12 Expand & Contract) :
//
//   - quota_exceeded_emitted_at_month  TIMESTAMP WITH TIME ZONE
//     Mois (timestamp au début du mois UTC) du dernier emit
//     tenant.quota_exceeded pour ce tenant. Mis à jour atomiquement par
//     MarkQuotaExceededEmitted quand l'incrément mensuel franchit le seuil
//     `monthly_email_quota` ET (la colonne est NULL OU le mois enregistré
//     est antérieur au mois courant).
//     NULL = jamais franchi (cas par défaut, y compris tenants antérieurs
//     à V40 — pas de backfill car -1 = illimité partout côté pivot pricing
//     2026-05-21, le webhook est de fait dormant tant qu'on ne réactive pas
//     les quotas Phase C Resend managé).
//
// Pas d'index : veridian_plan est petit (<10k rows), lookup par
// workspace_id (PRIMARY KEY).
type V40Migration struct{}

func (m *V40Migration) GetMajorVersion() float64 {
	return 40.0
}

func (m *V40Migration) HasSystemUpdate() bool {
	return true
}

func (m *V40Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V40Migration) ShouldRestartServer() bool {
	return false
}

func (m *V40Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS quota_exceeded_emitted_at_month TIMESTAMP WITH TIME ZONE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.quota_exceeded_emitted_at_month: %w", err)
	}
	return nil
}

func (m *V40Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V40Migration{})
}
