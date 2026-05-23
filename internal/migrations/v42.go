package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V42Migration ajoute la colonne `users.language` qui aurait dû être livrée
// par la fusion V32 (cherry-pick upstream 7a42651d "translate system emails
// with user language" du 2026-05-23).
//
// Pourquoi une migration dédiée plutôt qu'éditer V32 :
//
//	Notre V32Migration historique (Veridian patch `users.veridian_managed`)
//	avait déjà été appliquée en staging + prod bien avant le cherry-pick
//	upstream. Le migrator skip V32 dès que `db_version >= 32` — donc ajouter
//	l'`ALTER ADD language` dans V32 ne déclenche AUCUN runtime sur les bases
//	existantes. Résultat : code Go SELECT u.language → `pq: column
//	"language" does not exist` → CI E2E rouge (vu run 26330114345, 8 tests
//	@regression failed avec ce message).
//
// V42 est purement additive (Expand-only §12) : ADD COLUMN avec DEFAULT 'en'
// pour les rows existantes. La colonne est aussi déclarée dans V32 pour
// les NOUVELLES installations qui sautent direct au schéma cible — mais
// IF NOT EXISTS garantit l'idempotence si V42 court avant V32 (cas
// jamais atteint dans la pratique : V32 < V42 dans l'ordre).
//
// Pas de DROP, pas de NOT NULL sur rows existantes → safe-rollback :
// l'image précédente (qui ne SELECT pas u.language) continue à tourner
// sur ce schéma augmenté.
type V42Migration struct{}

func (m *V42Migration) GetMajorVersion() float64 {
	return 42.0
}

func (m *V42Migration) HasSystemUpdate() bool {
	return true
}

func (m *V42Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V42Migration) ShouldRestartServer() bool {
	return false
}

func (m *V42Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS language VARCHAR(10) NOT NULL DEFAULT 'en'
	`); err != nil {
		return fmt.Errorf("add users.language: %w", err)
	}
	return nil
}

func (m *V42Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V42Migration{})
}
