package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V52Migration ajoute le support de l'ANTI-HASH IDENTIQUE par classe de provider
// destinataire (cold outbound, ticket 2026-06-15) sur message_history (table par
// workspace) :
//
//   - colonne `veridian_content_hash CHAR(32)` (nullable) : le hash du rendu
//     final normalisé (sujet + corps), posé à l'enqueue/à l'envoi cold. NULL pour
//     tous les messages historiques et tous les envois non-cold (anti-hash
//     désactivé) — additif pur, aucune réécriture de données.
//
//   - index partiel `idx_message_history_content_hash_sent_at`
//     (veridian_content_hash, sent_at) WHERE veridian_content_hash IS NOT NULL :
//     sert l'EXISTS de collision dans la fenêtre glissante :
//     SELECT 1 WHERE veridian_content_hash = :h AND sent_at >= :since AND <classe>.
//     Le préfixe hash est ultra sélectif → EXISTS index-only. L'index PARTIEL
//     (WHERE NOT NULL) ne pèse que sur les envois cold qui portent un hash
//     (négligeable pour les workspaces non-cold : index quasi vide).
//
// Dégradation gracieuse classes MX (cohérence daily cap V49) : le filtre par
// classe se fait par liste de domaines (VeridianDomainsForClass), qui renvoie
// une liste vide pour les classes MX (ovh/ionos/…) → l'anti-hash ne s'enforce
// pas par ce chemin pour elles, EXACTEMENT comme le cap-classe journalier. Le
// risque d'empreinte est dominant sur google/microsoft/yahoo (suffixe connu),
// qui SONT couverts. Si l'anti-hash doit s'enforcer sur les classes MX, même
// décision lead que v49 : matérialiser la classe sur message_history.
//
// Safety §12 (Expand & Contract) : ADD COLUMN nullable + CREATE INDEX additifs
// purs, idempotents (IF NOT EXISTS). Le tag Docker précédent (V51) tourne sur ce
// schéma sans problème (il ignore la colonne/l'index). PAS de CONCURRENTLY : les
// migrations Notifuse tournent en transaction (manager.go BeginTx) et Postgres
// interdit CONCURRENTLY dans une TX. CREATE INDEX simple, même pattern que V49 et
// que tous les index message_history de init.go ; lock court borné par la taille
// de la table de CE workspace (multi-tenant par DB). Override safety §12 dans
// migrations-pending.txt (cf. memory reference_migration_index_concurrently_tx_trap).
//
// Pas de patch upstream : ajout via migration DB, aucun fichier upstream touché.
type V52Migration struct{}

func (m *V52Migration) GetMajorVersion() float64 {
	return 52.0
}

func (m *V52Migration) HasSystemUpdate() bool {
	return false
}

func (m *V52Migration) HasWorkspaceUpdate() bool {
	return true
}

func (m *V52Migration) ShouldRestartServer() bool {
	return false
}

func (m *V52Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V52Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	// Colonne hash de contenu (nullable, additive). IF NOT EXISTS : idempotent,
	// rejouable sans erreur sur un workspace déjà migré ou créé après init.go.
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE message_history
		ADD COLUMN IF NOT EXISTS veridian_content_hash CHAR(32)
	`); err != nil {
		return fmt.Errorf("add message_history.veridian_content_hash: %w", err)
	}

	// Index partiel pour l'EXISTS de collision dans la fenêtre glissante. Partiel
	// (WHERE NOT NULL) : ne pèse que sur les envois cold porteurs d'un hash.
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_message_history_content_hash_sent_at
		ON message_history(veridian_content_hash, sent_at)
		WHERE veridian_content_hash IS NOT NULL
	`); err != nil {
		return fmt.Errorf("create idx_message_history_content_hash_sent_at: %w", err)
	}

	return nil
}

func init() {
	Register(&V52Migration{})
}
