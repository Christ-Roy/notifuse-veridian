package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V50Migration crée la table SYSTÈME veridian_imap_uid_seen pour le tracking
// d'idempotence du poller IMAP self-service (Lot 1 sprint cold outbound,
// 2026-06-15). Cf. internal/domain/veridian_imap_integration.go,
// internal/service/queue/veridian_imap_poller.go.
//
// Pourquoi une table (et pas un store en mémoire) : le poller ne doit JAMAIS
// re-dispatcher un UID déjà traité (sinon les lots downstream — bounce-loop,
// stop-on-reply — re-déclencheraient leur effet métier à chaque redémarrage
// worker). Un suivi en mémoire se réinitialiserait au boot. La table garantit
// l'idempotence DURABLE, exactement comme message_history sert de source de
// vérité pour le plafond journalier (V49) plutôt qu'un compteur en mémoire.
//
// Clé (PK composite) : (workspace_id, integration_id, folder, uid_validity, uid).
//   - uid_validity est OBLIGATOIRE dans la clé (RFC 3501 §2.3.1.1) : si le
//     serveur IMAP change le UIDVALIDITY d'un dossier, les anciens UID ne
//     désignent plus les mêmes messages → on doit les re-considérer comme neufs.
//   - La PK sert directement le ON CONFLICT DO NOTHING (MarkSeen) et le SELECT
//     ... WHERE clé IN (...) (FilterUnseen) — pas besoin d'index supplémentaire.
//
// Table SYSTÈME (pas par workspace) : le poller itère TOUS les workspaces depuis
// la base système (WorkspaceRepository.List), et les colonnes workspace_id /
// integration_id portent le tenant dans la clé. Un seul endroit à interroger,
// pas de fan-out multi-DB pour un simple set d'UID vus.
//
// Safety §12 (Expand & Contract) : CREATE TABLE IF NOT EXISTS = additif pur. Le
// tag Docker précédent (V49) tourne sur ce schéma sans problème (il ne lit pas
// la table). AccessExclusiveLock court (création table vide). La PK composite
// couvre tous les accès (FilterUnseen + MarkSeen) — aucun index secondaire
// nécessaire, donc aucun lock long sur une table existante.
//
// Idempotent : IF NOT EXISTS sur CREATE TABLE. Re-run sans effet.
//
// Aucun fichier upstream touché par cette migration (création de table dédiée
// préfixée veridian_, convention respectée).
type V50Migration struct{}

func (m *V50Migration) GetMajorVersion() float64 {
	return 50.0
}

func (m *V50Migration) HasSystemUpdate() bool {
	return true
}

func (m *V50Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V50Migration) ShouldRestartServer() bool {
	return false
}

func (m *V50Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS veridian_imap_uid_seen (
			workspace_id   TEXT   NOT NULL,
			integration_id TEXT   NOT NULL,
			folder         TEXT   NOT NULL,
			uid_validity   BIGINT NOT NULL,
			uid            BIGINT NOT NULL,
			seen_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			PRIMARY KEY (workspace_id, integration_id, folder, uid_validity, uid)
		)
	`); err != nil {
		return fmt.Errorf("create veridian_imap_uid_seen: %w", err)
	}
	return nil
}

func (m *V50Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V50Migration{})
}
