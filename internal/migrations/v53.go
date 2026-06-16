package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V53Migration ajoute le support du PLAFOND JOURNALIER PAR ADRESSE ÉMETTRICE
// (warmup IP, cold outbound, ticket 2026-06-16-cap-par-sender-emetteur) sur
// message_history (table par workspace) :
//
//   - colonne `veridian_sender_email VARCHAR(255)` (nullable) : l'adresse FROM
//     réelle de l'envoi (EmailQueuePayload.FromAddress), stockée lowercase. NULL
//     pour tous les messages historiques et tout envoi sans FROM connu — additif
//     pur, aucune réécriture de données. C'est la première dimension ÉMETTRICE
//     exploitable d'un COUNT : avant V53, message_history ne stockait que le
//     destinataire (contact_email) — le FROM ne vivait que dans le JSON
//     channel_options (FromName = display name, non-queryable).
//
//   - index partiel `idx_message_history_sender_email_sent_at`
//     (veridian_sender_email, sent_at) WHERE veridian_sender_email IS NOT NULL :
//     sert le COUNT du cap par sender depuis minuit UTC :
//     SELECT COUNT(*) WHERE veridian_sender_email = :s AND sent_at >= :since.
//     Le préfixe sender est ultra sélectif (peu de boîtes d'envoi par infra) →
//     COUNT index-only. L'index PARTIEL (WHERE NOT NULL) ne pèse que sur les
//     envois cold porteurs d'un FROM tracé (négligeable pour les workspaces
//     non-cold : index quasi vide, comme le content_hash V52).
//
// Safety §12 (Expand & Contract) : ADD COLUMN nullable + CREATE INDEX additifs
// purs, idempotents (IF NOT EXISTS). Le tag Docker précédent (V52) tourne sur ce
// schéma sans problème (il ignore la colonne/l'index). PAS de CONCURRENTLY : les
// migrations Notifuse tournent en transaction (manager.go BeginTx) et Postgres
// interdit CONCURRENTLY dans une TX. CREATE INDEX simple, même pattern que V49/V52
// et que tous les index message_history de init.go ; lock court borné par la
// taille de la table de CE workspace (multi-tenant par DB). Override safety §12
// dans migrations-pending.txt (cf. memory reference_migration_index_concurrently_tx_trap).
//
// Pas de patch upstream : ajout via migration DB, aucun fichier upstream touché.
type V53Migration struct{}

func (m *V53Migration) GetMajorVersion() float64 {
	return 53.0
}

func (m *V53Migration) HasSystemUpdate() bool {
	return false
}

func (m *V53Migration) HasWorkspaceUpdate() bool {
	return true
}

func (m *V53Migration) ShouldRestartServer() bool {
	return false
}

func (m *V53Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V53Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	// Colonne adresse émettrice (nullable, additive). IF NOT EXISTS : idempotent,
	// rejouable sans erreur sur un workspace déjà migré ou créé après init.go.
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE message_history
		ADD COLUMN IF NOT EXISTS veridian_sender_email VARCHAR(255)
	`); err != nil {
		return fmt.Errorf("add message_history.veridian_sender_email: %w", err)
	}

	// Index partiel pour le COUNT du cap journalier par sender. Partiel
	// (WHERE NOT NULL) : ne pèse que sur les envois cold porteurs d'un FROM tracé.
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_message_history_sender_email_sent_at
		ON message_history(veridian_sender_email, sent_at)
		WHERE veridian_sender_email IS NOT NULL
	`); err != nil {
		return fmt.Errorf("create idx_message_history_sender_email_sent_at: %w", err)
	}

	return nil
}

func init() {
	Register(&V53Migration{})
}
