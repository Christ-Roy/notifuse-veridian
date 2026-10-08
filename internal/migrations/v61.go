package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V61Migration ajoute le type de message a la table WORKSPACE message_history
// (lot 4, separation transactionnel / commercial, 08/10/2026) : colonne nullable
// veridian_message_type, 'transactional' pour un mail transactionnel (API d'envoi,
// relais SMTP, modele transactionnel envoye par une sequence), NULL pour le
// commercial et pour tout l'historique. Les compteurs commerciaux (plafonds,
// chauffe, fusible de reputation) et les metriques separees la lisent.
//
// Safety (Expand & Contract, Constitution section 12) : ADD COLUMN IF NOT EXISTS
// nullable, sans DEFAULT ni index : metadonnee seule, aucun rewrite de table,
// verrou tres court. Le tag precedent (V60) tourne sur ce schema sans probleme (il
// ignore la colonne). Idempotent.
type V61Migration struct{}

func (m *V61Migration) GetMajorVersion() float64 { return 61.0 }

func (m *V61Migration) HasSystemUpdate() bool { return false }

func (m *V61Migration) HasWorkspaceUpdate() bool { return true }

func (m *V61Migration) ShouldRestartServer() bool { return false }

func (m *V61Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V61Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE message_history ADD COLUMN IF NOT EXISTS veridian_message_type VARCHAR(16)`); err != nil {
		return fmt.Errorf("add message_history.veridian_message_type: %w", err)
	}
	return nil
}

func init() { Register(&V61Migration{}) }
