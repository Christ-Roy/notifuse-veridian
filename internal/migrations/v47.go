package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V47Migration cree la table veridian_frozen_members pour le freeze per-user
// (CONTRAT-HUB §5.21 — seat overage soft warning). Quand le Hub depasse son
// quota seats sur un tenant, il peut envoyer un freeze sur les derniers
// invites via POST /api/tenants/{id}/freeze-member. Ces users passent en mode
// degrade paywall (lecture obfusquee, ecritures 402 user_frozen) sans etre
// supprimes du workspace. Reversible via unfreeze.
//
// Pourquoi table dediee plutot qu'une colonne sur user_workspaces :
//   - Zero patch upstream (regle CLAUDE.md Notifuse "jamais patcher un fichier
//     upstream") : user_workspaces est un table/domain upstream.
//   - Index naturel (workspace_id, user_id) PK composite + lookups directs
//     par le middleware paywall sans toucher au schema upstream.
//   - Permet d'ajouter `reason` + `frozen_by_hub_user_id` plus tard sans risque
//     de collision avec une evolution upstream.
//
// Schema :
//
//   workspace_id          TEXT NOT NULL    — id workspace (FK logique workspaces.id)
//   user_id               TEXT NOT NULL    — id user Notifuse (FK logique users.id)
//   frozen_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()  — moment du freeze
//   reason                TEXT NOT NULL    — quota_seat_exceeded | manual | ...
//   PRIMARY KEY (workspace_id, user_id)
//
// Pas d'index supplementaire : la PK composite couvre le lookup principal
// (middleware : SELECT FROM veridian_frozen_members WHERE workspace_id=$1 AND
// user_id=$2). Pas d'index sur frozen_at seul (pas de query "list all frozen
// across tenants" prevue).
//
// Pas de CREATE INDEX : CONCURRENTLY interdit dans la TX manager
// (cf. v41.go meme rationale), et CREATE INDEX sans CONCURRENTLY refuse par
// check-migration-safety.sh §12 Expand & Contract.
//
// Safety §12 (Expand & Contract) : CREATE TABLE IF NOT EXISTS = additif pur.
// Le tag Docker precedent (V46) tourne sur ce schema sans probleme (il ne lit
// pas la table). AccessExclusiveLock court (creation table vide).
//
// Idempotent : IF NOT EXISTS sur CREATE TABLE. Re-run sans effet.
//
// Note : Hub n'emet pas encore `tenant.member_frozen` cross-app en mai 2026.
// L'implementation est shippee proactivement pour qu'au moment ou le Hub
// branchera le webhook, l'app reponde immediatement (pas de course
// reciproque a livrer en parallele).
type V47Migration struct{}

func (m *V47Migration) GetMajorVersion() float64 {
	return 47.0
}

func (m *V47Migration) HasSystemUpdate() bool {
	return true
}

func (m *V47Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V47Migration) ShouldRestartServer() bool {
	return false
}

func (m *V47Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS veridian_frozen_members (
			workspace_id TEXT NOT NULL,
			user_id      TEXT NOT NULL,
			frozen_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			reason       TEXT NOT NULL,
			PRIMARY KEY (workspace_id, user_id)
		)
	`); err != nil {
		return fmt.Errorf("create veridian_frozen_members: %w", err)
	}
	return nil
}

func (m *V47Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V47Migration{})
}
