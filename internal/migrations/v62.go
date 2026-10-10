package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V62Migration (fiche 62, lot 1 « pourquoi ca n'envoie pas », 10/10/2026) :
//   - email_queue gagne la RAISON du dernier report (defer_reason, defer_detail,
//     defer_profile), son horodatage et son compteur, les horodatages d'examen, le
//     noeud email (node_id) et decision_logged_at (rythme des battements du journal) ;
//   - nouvelle table veridian_send_decisions : le journal des decisions d'envoi (une
//     ligne par decision marquante : gates evalues avec valeur, limite et verdict).
//
// Safety (Expand & Contract, Constitution section 12) : ADD COLUMN IF NOT EXISTS
// nullables, sans DEFAULT ni index (metadonnee seule, aucun rewrite, verrou tres court)
// et CREATE TABLE / INDEX IF NOT EXISTS sur une table NEUVE (vide : les index sont
// instantanes, CONCURRENTLY est impossible dans la transaction du runner, cf.
// migrations-pending.txt). Le tag precedent (V61) tourne sur ce schema : il ignore les
// colonnes et la table. Idempotent.
type V62Migration struct{}

func (m *V62Migration) GetMajorVersion() float64 { return 62.0 }

func (m *V62Migration) HasSystemUpdate() bool { return false }

func (m *V62Migration) HasWorkspaceUpdate() bool { return true }

func (m *V62Migration) ShouldRestartServer() bool { return false }

func (m *V62Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V62Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE email_queue
			ADD COLUMN IF NOT EXISTS node_id VARCHAR(36),
			ADD COLUMN IF NOT EXISTS defer_reason VARCHAR(24),
			ADD COLUMN IF NOT EXISTS defer_detail VARCHAR(160),
			ADD COLUMN IF NOT EXISTS defer_profile VARCHAR(36),
			ADD COLUMN IF NOT EXISTS deferred_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS defer_count INTEGER,
			ADD COLUMN IF NOT EXISTS first_examined_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS last_examined_at TIMESTAMPTZ,
			ADD COLUMN IF NOT EXISTS decision_logged_at TIMESTAMPTZ`); err != nil {
		return fmt.Errorf("add email_queue deferral columns: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS veridian_send_decisions (
			id VARCHAR(36) PRIMARY KEY,
			at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			entry_id VARCHAR(36),
			message_id VARCHAR(100),
			contact_email VARCHAR(255) NOT NULL,
			automation_id VARCHAR(36),
			node_id VARCHAR(36),
			outcome VARCHAR(16) NOT NULL,
			reason VARCHAR(32),
			detail VARCHAR(200),
			until TIMESTAMPTZ,
			profile_id VARCHAR(36),
			sampled BOOLEAN NOT NULL DEFAULT FALSE,
			trace JSONB
		)`); err != nil {
		return fmt.Errorf("create veridian_send_decisions: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_veridian_send_decisions_contact ON veridian_send_decisions(contact_email, at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_veridian_send_decisions_node ON veridian_send_decisions(automation_id, node_id, at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_veridian_send_decisions_entry ON veridian_send_decisions(entry_id)`,
		`CREATE INDEX IF NOT EXISTS idx_veridian_send_decisions_at ON veridian_send_decisions(at)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create veridian_send_decisions index: %w", err)
		}
	}
	return nil
}

func init() { Register(&V62Migration{}) }
