package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V60Migration ajoute le type de reponse a la table WORKSPACE veridian_contact_reply
// (2026-10-06) : human / auto / challenge. Cf. internal/domain/veridian_reply_type.go.
//
// Constat : sur la campagne du 28/09, des auto-repondeurs, un filtre anti-spam et un
// defi Mailinblack ont marque 'replied' des prospects qui n'avaient rien repondu et les
// ont sortis de sequence. Seule une reponse humaine doit arreter la cadence ; le
// repository lit desormais reply_type = 'human' pour HasReplied et le taux de reponse.
//
// Safety (Expand & Contract, section 12) : ADD COLUMN IF NOT EXISTS avec un DEFAULT
// constant. Postgres 11+ ne reecrit pas la table pour un DEFAULT constant, verrou
// tres court. Les lignes existantes valent 'human' (comportement anterieur : toutes
// comptaient comme reponses). Le tag precedent (V59) tourne sur ce schema sans
// probleme (il ne lit pas la colonne). Idempotent : IF NOT EXISTS.
type V60Migration struct{}

func (m *V60Migration) GetMajorVersion() float64 { return 60.0 }

func (m *V60Migration) HasSystemUpdate() bool { return false }

func (m *V60Migration) HasWorkspaceUpdate() bool { return true }

func (m *V60Migration) ShouldRestartServer() bool { return false }

func (m *V60Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V60Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE veridian_contact_reply
		ADD COLUMN IF NOT EXISTS reply_type TEXT NOT NULL DEFAULT 'human'
	`); err != nil {
		return fmt.Errorf("add veridian_contact_reply.reply_type: %w", err)
	}
	return nil
}

func init() { Register(&V60Migration{}) }
