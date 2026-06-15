package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V51Migration crée la table WORKSPACE veridian_contact_reply : source de vérité du
// signal "ce contact a répondu" pour le stop-on-reply cold (Lot 3 sprint cold outbound,
// 2026-06-15). Cf. internal/domain/veridian_contact_reply.go,
// internal/service/veridian_reply_service.go.
//
// Pourquoi une table (et pas un store en mémoire / un custom field / contact_lists) :
//   - DURABLE : le signal doit survivre aux redémarrages worker. Le gate d'exit de
//     séquence (Lot 9) le relit à chaque tick ; un store RAM se réinitialiserait et
//     re-relancerait des prospects ayant déjà répondu.
//   - DÉDIÉ : custom_string_5 est occupé (classe provider du tunnel) ; contact_lists.status
//     porte la sémantique d'abonnement, pas l'engagement prospect. Mélanger casserait
//     IsTerminalContactListStatus / le sens upstream.
//   - IDEMPOTENT : PK (contact_email) + ON CONFLICT DO NOTHING → le premier signal gagne,
//     un re-dispatch IMAP (MarkSeen raté) ne double rien.
//
// Table WORKSPACE : donnée métier-contact, vit dans la DB du workspace comme contacts /
// contact_lists / message_history. UpdateWorkspace s'exécute pour chaque workspace ;
// init.go porte le même CREATE pour les workspaces créés APRÈS cette version.
//
// Colonnes :
//   - contact_email       : PK, l'adresse normalisée (lowercase) du prospect ayant répondu.
//   - replied_at          : instant de la première réponse détectée (Date du mail, ou
//                           now() si absente/aberrante côté consumer).
//   - match_type          : 'message_id' (signal fort In-Reply-To/References) ou
//                           'sender_fallback' (From = contact connu, pas de threading).
//   - matched_message_id  : id message_history de l'envoi cité (NULL en fallback) — audit.
//   - created_at          : insertion DB.
//
// Safety §12 (Expand & Contract) : CREATE TABLE IF NOT EXISTS = additif pur. Le tag
// Docker précédent (V50) tourne sur ce schéma sans problème (il ne lit pas la table).
// AccessExclusiveLock court (table vide). La PK (contact_email) couvre les deux accès
// (MarkReplied upsert + HasReplied lookup) — aucun index secondaire nécessaire.
//
// Idempotent : IF NOT EXISTS sur CREATE TABLE. Re-run sans effet.
//
// Aucun fichier upstream touché (table dédiée préfixée veridian_, convention respectée).
type V51Migration struct{}

func (m *V51Migration) GetMajorVersion() float64 {
	return 51.0
}

func (m *V51Migration) HasSystemUpdate() bool {
	return false
}

func (m *V51Migration) HasWorkspaceUpdate() bool {
	return true
}

func (m *V51Migration) ShouldRestartServer() bool {
	return false
}

func (m *V51Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V51Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS veridian_contact_reply (
			contact_email      TEXT NOT NULL,
			replied_at         TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			match_type         TEXT NOT NULL,
			matched_message_id TEXT,
			created_at         TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			PRIMARY KEY (contact_email)
		)
	`); err != nil {
		return fmt.Errorf("create veridian_contact_reply: %w", err)
	}
	return nil
}

func init() {
	Register(&V51Migration{})
}
