package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V59Migration cree la table agent_install_tokens — mission "API & agents"
// (2026-10-03) : brancher un agent IA sur un workspace en une commande
// copier-coller, sans que la clé API n'apparaisse en clair dans l'historique
// du shell. Voir internal/domain/agent_install.go pour le cycle de vie complet.
//
// Schema :
//
//	token_hash        TEXT PRIMARY KEY  — sha256 hex du jeton brut (jamais
//	                                      stocké en clair, non devinable :
//	                                      256 bits d'entropie côté client)
//	workspace_id      TEXT NOT NULL
//	api_key_user_id   TEXT NOT NULL     — l'api_key mintée pour cet agent
//	encrypted_api_key TEXT NOT NULL     — clé API chiffrée (AES-GCM, passphrase
//	                                      = SecretKey), déchiffrée une seule
//	                                      fois à l'échange puis jamais relue
//	created_by        TEXT NOT NULL     — user_id du owner/admin qui a miné le jeton
//	created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
//	expires_at        TIMESTAMPTZ NOT NULL — courte durée (10 min côté service)
//	used_at           TIMESTAMPTZ        — NULL jusqu'à consommation (usage unique)
//
// Table système (comme veridian_api_key_grace, V41) : les jetons ne sont pas
// des données de contenu workspace, ils vivent dans la DB système qui porte
// déjà users/user_workspaces.
//
// Pas d'index supplémentaire : rows transientes (10min), volume faible,
// lookup par clé primaire token_hash déjà indexé. Aucune commande de
// création d'index à ajouter ici, donc aucun souci de verrou de table
// (ce type de commande, pris sans option de concurrence, est interdit
// dans la transaction de migration — cf. v41.go, non applicable ici).
//
// Additive, idempotent (IF NOT EXISTS). Pas de DROP.
type V59Migration struct{}

func (m *V59Migration) GetMajorVersion() float64  { return 59.0 }
func (m *V59Migration) HasSystemUpdate() bool     { return true }
func (m *V59Migration) HasWorkspaceUpdate() bool  { return false }
func (m *V59Migration) ShouldRestartServer() bool { return false }

func (m *V59Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS agent_install_tokens (
			token_hash        TEXT PRIMARY KEY,
			workspace_id      TEXT NOT NULL,
			api_key_user_id   TEXT NOT NULL,
			encrypted_api_key TEXT NOT NULL,
			created_by        TEXT NOT NULL,
			created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			expires_at        TIMESTAMP WITH TIME ZONE NOT NULL,
			used_at           TIMESTAMP WITH TIME ZONE
		)
	`); err != nil {
		return fmt.Errorf("create agent_install_tokens: %w", err)
	}

	return nil
}

func (m *V59Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() { Register(&V59Migration{}) }
