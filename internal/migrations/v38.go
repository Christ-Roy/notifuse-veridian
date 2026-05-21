package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V38Migration ajoute les colonnes lifetime email tracking sur veridian_plan.
//
// Colonnes (additives, IF NOT EXISTS, cf. Constitution sec.12 Expand & Contract) :
//
//   - emails_sent_lifetime  BIGINT NOT NULL DEFAULT 0
//     Compteur cumulatif des emails envoyés par ce tenant — jamais remis à
//     zéro (contrairement à emails_sent_this_month qui se reset mensuellement).
//     Signal d'activation business : 5 mails = le tenant "utilise vraiment"
//     l'outil. Cf. ticket todo/2026-05-21-trial-eligible-signal.md.
//
//   - activity_threshold_reached_at  TIMESTAMP WITH TIME ZONE
//     Timestamp de franchissement du seuil 5 mails. NULL tant que le seuil
//     n'est pas atteint. Écrit une seule fois (idempotent par WHERE IS NULL)
//     pour garantir que le webhook tenant.activity_threshold_reached n'est
//     émis qu'une seule fois par tenant. Le Hub consomme ce signal pour
//     démarrer le timer trial 2j → 15j.
//
// Backfill : emails_sent_lifetime = emails_sent_this_month (approximation
// safe — sous-estime pour les tenants qui ont vu un reset mensuel, mais le
// seuil 5 est trivial à atteindre de toute façon). Décision : on accepte
// l'imprécision (cf. ticket §risques).
//
// Pas d'index : veridian_plan est petit (<10k rows), lookup par workspace_id
// (PRIMARY KEY) — pas de besoin d'index secondaire sur ces colonnes.
type V38Migration struct{}

func (m *V38Migration) GetMajorVersion() float64 {
	return 38.0
}

func (m *V38Migration) HasSystemUpdate() bool {
	return true
}

func (m *V38Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V38Migration) ShouldRestartServer() bool {
	return false
}

func (m *V38Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS emails_sent_lifetime BIGINT NOT NULL DEFAULT 0
	`); err != nil {
		return fmt.Errorf("add veridian_plan.emails_sent_lifetime: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS activity_threshold_reached_at TIMESTAMP WITH TIME ZONE
	`); err != nil {
		return fmt.Errorf("add veridian_plan.activity_threshold_reached_at: %w", err)
	}

	// Backfill : initialiser emails_sent_lifetime = emails_sent_this_month
	// pour les tenants existants. Approximation safe (sous-estime en cas de
	// reset mensuel passé, mais acceptable pour le seuil 5). Idempotent :
	// si rejoué, les tenants qui ont déjà un lifetime > 0 restent inchangés
	// car on applique seulement WHERE emails_sent_lifetime = 0.
	if _, err := db.ExecContext(ctx, `
		UPDATE veridian_plan
		SET emails_sent_lifetime = emails_sent_this_month
		WHERE emails_sent_lifetime = 0 AND emails_sent_this_month > 0
	`); err != nil {
		return fmt.Errorf("backfill veridian_plan emails_sent_lifetime: %w", err)
	}

	return nil
}

func (m *V38Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V38Migration{})
}
