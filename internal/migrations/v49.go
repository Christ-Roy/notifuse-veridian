package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V49Migration ajoute deux index sur message_history (table par workspace) pour
// le PLAFOND JOURNALIER d'envoi cold outbound (ticket R0 2026-06-14) :
//
//   - idx_message_history_contact_email_sent_at (contact_email, sent_at)
//     sert le COUNT du cap par DESTINATAIRE (anti-harcèlement) :
//     SELECT COUNT(*) WHERE contact_email = :email AND sent_at >= minuit.
//     Le préfixe contact_email est ultra sélectif → COUNT index-only.
//     (Un index (contact_email) seul existe déjà depuis init.go ; le composite
//     évite le filtre résiduel sur sent_at et rend le COUNT index-only.)
//
//   - idx_message_history_sent_at (sent_at)
//     sert le COUNT du cap par CLASSE (réputation) :
//     SELECT COUNT(*) WHERE sent_at >= minuit AND <domaine ∈/∉ classe>.
//     Le filtre par domaine se fait sur split_part(contact_email,'@',2) (la
//     classe n'est pas matérialisée en DB, décision V1) ; l'index borne d'abord
//     par la fenêtre journalière, le scan résiduel est négligeable sur le volume
//     cold quotidien. Si ce cap devient un point chaud, on matérialisera la
//     classe (colonne + index) — pas avant d'en mesurer le besoin.
//
// Safety §12 (Expand & Contract) : CREATE INDEX additif pur, idempotent
// (IF NOT EXISTS). Le tag Docker précédent (V48) tourne sur ce schéma sans
// problème (il n'utilise pas ces index). PAS de CONCURRENTLY : les migrations
// Notifuse tournent en transaction (cf. manager.go BeginTx) et Postgres interdit
// CONCURRENTLY dans une TX. CREATE INDEX simple prend un AccessExclusiveLock
// court sur message_history ; même pattern que les index message_history
// additifs existants (V21 idx_broadcast_id, V28 idx_transactional_notification_id),
// acceptable sur le volume cold outbound. Si message_history devient très grosse
// avant que cet index existe sur un workspace donné, le lock reste borné par la
// taille de la table de CE workspace (multi-tenant par DB).
//
// Pas de patch upstream : ajout d'index via migration DB, aucun fichier upstream
// touché. Convention veridian respectée (la migration vit au niveau migrations,
// numérotée à la suite).
type V49Migration struct{}

func (m *V49Migration) GetMajorVersion() float64 {
	return 49.0
}

func (m *V49Migration) HasSystemUpdate() bool {
	return false
}

func (m *V49Migration) HasWorkspaceUpdate() bool {
	return true
}

func (m *V49Migration) ShouldRestartServer() bool {
	return false
}

func (m *V49Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V49Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	// Index pour le cap par destinataire (COUNT contact_email + fenêtre jour).
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_message_history_contact_email_sent_at
		ON message_history(contact_email, sent_at)
	`); err != nil {
		return fmt.Errorf("create idx_message_history_contact_email_sent_at: %w", err)
	}

	// Index pour le cap par classe (COUNT sur la fenêtre journalière).
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_message_history_sent_at
		ON message_history(sent_at)
	`); err != nil {
		return fmt.Errorf("create idx_message_history_sent_at: %w", err)
	}

	return nil
}

func init() {
	Register(&V49Migration{})
}
