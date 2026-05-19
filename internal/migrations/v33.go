package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V33Migration ajoute la colonne `veridian_plan.plan_source` (CONTRAT-HUB
// sec. 3.3 + sec. 5.2). Source du plan tenant :
//
//   - stripe                  : plan paye, source de verite = webhook Stripe (Hub)
//   - manual                  : assigne par Robert, peut etre annule a la main
//   - lifetime_site_vitrine   : offert via le site vitrine (immutable face a Stripe)
//   - lifetime_partner        : offert a un partenaire (immutable face a Stripe)
//   - internal                : usage interne Veridian
//
// Les 3 dernieres sont immunes aux downgrades automatiques en provenance du
// Hub : si le service recoit un update-plan avec plan_source=stripe alors que
// le plan existant est lifetime_*/internal, il refuse (409 plan_locked).
// Cela evite qu'un cron Stripe ecrase un plan offert manuellement.
//
// Backfill : tous les tenants existants sont marques 'stripe' par defaut, ce
// qui matche le comportement actuel (rien d'immune en prod aujourd'hui). Si
// Robert avait deja offert un plan a un tenant, il faudra updater plan_source
// a la main apres deploy (UPDATE veridian_plan SET plan_source = '...' WHERE
// workspace_id = '...').
type V33Migration struct{}

func (m *V33Migration) GetMajorVersion() float64 {
	return 33.0
}

func (m *V33Migration) HasSystemUpdate() bool {
	return true
}

func (m *V33Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V33Migration) ShouldRestartServer() bool {
	return false
}

func (m *V33Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_plan
		ADD COLUMN IF NOT EXISTS plan_source VARCHAR(32) NOT NULL DEFAULT 'stripe'
	`); err != nil {
		return fmt.Errorf("add veridian_plan.plan_source: %w", err)
	}
	// Pas d'index sur plan_source ici : les migrations Notifuse tournent en
	// transaction (cf. internal/migrations/manager.go:201 BeginTx) et la
	// version CONCURRENTLY ne peut pas etre dans une TX. Le scan full table
	// est acceptable pour les requetes audit "plans offerts" tant que
	// veridian_plan reste petit (<10k tenants). Si Robert finit par avoir
	// des centaines de milliers de tenants ET besoin de cette requete, on
	// ajoutera l'index via une migration hors-TX dediee.
	return nil
}

func (m *V33Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V33Migration{})
}
