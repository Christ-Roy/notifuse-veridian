package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V60Migration -- mission 2026-10-04 (audit backend, item 5/basse).
//
// veridian_idempotency_keys (V35) porte deja une colonne tenant_id, mais
// PURE TRACEABILITY : la cle primaire reste "key" seul, et le middleware
// (internal/http/middleware/veridian_idempotency.go) fait son lookup par
// `key` SANS filtrer par tenant_id. Consequence mesuree : deux appelants
// HMAC differents (ou deux tenants) qui choisissent par coincidence la MEME
// valeur d'Idempotency-Key recoivent l'un la reponse cachee de l'AUTRE —
// un rejeu cross-tenant, pas juste un bug de perf.
//
// EXPAND phase (cette migration) : ajoute un index composite (tenant_id,
// key) pour servir les lookups desormais scopes par tenant AJOUTES par ce
// meme correctif cote code (Get prend un tenantID, WHERE key = $1 AND
// (tenant_id = $2 OR ($2 = '' AND tenant_id IS NULL))). Purement additive,
// IF NOT EXISTS, aucune donnee touchee, la PK "key" seule n'est PAS retiree
// ici.
//
// CONTRACT phase (deliberement PAS fait ici, suivi separe) : remplacer la PK
// par une vraie cle composite (tenant_id, key) pour permettre a deux tenants
// de choisir LEGITIMEMENT la meme valeur de cle cote client (aujourd'hui,
// deux tenants avec la meme valeur de cle se bloquent mutuellement sur un
// conflit PK -- un refus bruyant, pas une fuite, acceptable en etat
// intermediaire). Ce changement de PK touche une contrainte vivante en prod
// et merite sa propre migration verifiee separement.
type V60Migration struct{}

func (m *V60Migration) GetMajorVersion() float64  { return 60.0 }
func (m *V60Migration) HasSystemUpdate() bool     { return true }
func (m *V60Migration) HasWorkspaceUpdate() bool  { return false }
func (m *V60Migration) ShouldRestartServer() bool { return false }

func (m *V60Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_veridian_idempotency_tenant_key
		ON veridian_idempotency_keys (tenant_id, key)
	`); err != nil {
		return fmt.Errorf("create idx_veridian_idempotency_tenant_key: %w", err)
	}

	return nil
}

func (m *V60Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() { Register(&V60Migration{}) }
