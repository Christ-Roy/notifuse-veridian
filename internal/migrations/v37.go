package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V37Migration etend veridian_plan avec les dimensions du pricing
// Free / Pro / Business / Enterprise (cf. VISION-BUSINESS.md + ticket
// todo/2026-05-20-pricing-plans-implementation.md).
//
// Colonnes ajoutees (toutes additives, IF NOT EXISTS, NOT NULL avec
// DEFAULT — safe Constitution sec.12 Expand & Contract) :
//
//   - max_contacts            INTEGER  default 500   (Free)
//   - max_seats               INTEGER  default 1
//   - max_oauth_accounts      INTEGER  default 1
//   - max_custom_domains      INTEGER  default 0
//   - max_active_sequences    INTEGER  default 1
//   - feature_ab_testing      BOOLEAN  default false
//   - feature_branding_removed BOOLEAN default false
//   - feature_white_label     BOOLEAN  default false
//   - history_retention_days  INTEGER  default 30
//
// Convention -1 = illimite (semantique partagee avec MonthlyEmailQuota).
//
// Backfill par plan : les rows existantes sont initialement collees aux
// defaults (= Free). Un UPDATE … CASE plan WHEN 'pro' ... applique les
// limites adequates pour les tenants deja en pro/business/enterprise.
//
// Pas d'index : veridian_plan est petit (<10k rows), les checks de quota
// se font par workspace_id (PRIMARY KEY).
//
// Cf. ticket : todo/2026-05-20-pricing-plans-implementation.md livrable 1.
// V36 est reservee a l'alignement des types TIMESTAMP (ticket
// 2026-05-19-aligner-types-timestamp-veridian-plan.md) — donc V37 est
// bien la prochaine version pricing.
type V37Migration struct{}

func (m *V37Migration) GetMajorVersion() float64 {
	return 37.0
}

func (m *V37Migration) HasSystemUpdate() bool {
	return true
}

func (m *V37Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V37Migration) ShouldRestartServer() bool {
	return false
}

func (m *V37Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS max_contacts INTEGER NOT NULL DEFAULT 500
	`); err != nil {
		return fmt.Errorf("add veridian_plan.max_contacts: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS max_seats INTEGER NOT NULL DEFAULT 1
	`); err != nil {
		return fmt.Errorf("add veridian_plan.max_seats: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS max_oauth_accounts INTEGER NOT NULL DEFAULT 1
	`); err != nil {
		return fmt.Errorf("add veridian_plan.max_oauth_accounts: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS max_custom_domains INTEGER NOT NULL DEFAULT 0
	`); err != nil {
		return fmt.Errorf("add veridian_plan.max_custom_domains: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS max_active_sequences INTEGER NOT NULL DEFAULT 1
	`); err != nil {
		return fmt.Errorf("add veridian_plan.max_active_sequences: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS feature_ab_testing BOOLEAN NOT NULL DEFAULT FALSE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.feature_ab_testing: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS feature_branding_removed BOOLEAN NOT NULL DEFAULT FALSE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.feature_branding_removed: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS feature_white_label BOOLEAN NOT NULL DEFAULT FALSE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.feature_white_label: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS history_retention_days INTEGER NOT NULL DEFAULT 30
	`); err != nil {
		return fmt.Errorf("add veridian_plan.history_retention_days: %w", err)
	}

	// Backfill par plan — n'ecrase QUE les rows encore aux defaults Free
	// (max_contacts = 500), pour ne pas casser un eventuel custom deja
	// applique en hot-fix manuel. Idempotent : si rejoue, les rows
	// pro/business/enterprise sont deja remontees → no-op.
	if _, err := db.ExecContext(ctx, `
		UPDATE veridian_plan
		SET
			max_contacts = CASE plan
				WHEN 'pro' THEN 5000
				WHEN 'business' THEN 25000
				WHEN 'enterprise' THEN -1
				ELSE 500 END,
			max_seats = CASE plan
				WHEN 'pro' THEN 5
				WHEN 'business' THEN 25
				WHEN 'enterprise' THEN -1
				ELSE 1 END,
			max_oauth_accounts = CASE plan
				WHEN 'pro' THEN 5
				WHEN 'business' THEN 25
				WHEN 'enterprise' THEN -1
				ELSE 1 END,
			max_custom_domains = CASE plan
				WHEN 'pro' THEN 1
				WHEN 'business' THEN 5
				WHEN 'enterprise' THEN -1
				ELSE 0 END,
			max_active_sequences = CASE plan
				WHEN 'pro' THEN -1
				WHEN 'business' THEN -1
				WHEN 'enterprise' THEN -1
				ELSE 1 END,
			feature_ab_testing = CASE plan
				WHEN 'pro' THEN TRUE
				WHEN 'business' THEN TRUE
				WHEN 'enterprise' THEN TRUE
				ELSE FALSE END,
			feature_branding_removed = CASE plan
				WHEN 'pro' THEN TRUE
				WHEN 'business' THEN TRUE
				WHEN 'enterprise' THEN TRUE
				ELSE FALSE END,
			feature_white_label = CASE plan
				WHEN 'business' THEN TRUE
				WHEN 'enterprise' THEN TRUE
				ELSE FALSE END,
			history_retention_days = CASE plan
				WHEN 'pro' THEN 365
				WHEN 'business' THEN -1
				WHEN 'enterprise' THEN -1
				ELSE 30 END
		WHERE plan IN ('pro', 'business', 'enterprise')
	`); err != nil {
		return fmt.Errorf("backfill veridian_plan limits: %w", err)
	}

	return nil
}

func (m *V37Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V37Migration{})
}
