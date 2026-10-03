package domain

import (
	"context"
	"errors"
	"time"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===
//
// Permet à un utilisateur de brancher un agent IA (Claude Code ou
// équivalent) sur son workspace Notifuse en une seule commande
// copier-coller, SANS que la clé API n'apparaisse jamais en clair dans
// l'historique du shell : on échange un jeton d'installation à usage
// unique, à courte durée de vie, contre la vraie clé API, côté serveur.
//
// Cycle de vie :
//  1. POST /api/workspaces.createAgentInstallToken (authentifié, owner/admin)
//     → mint une NOUVELLE clé API (même mécanisme que CreateAPIKey) +
//       un jeton aléatoire 256 bits, stocké sous forme de hash (jamais en
//       clair), avec la clé API chiffrée (AES-GCM, passphrase = secretKey).
//  2. GET /agent/install.sh | sh -s -- --token <jeton>  (public, pas de clé
//     dans la commande ni dans l'historique shell)
//  3. POST /api/agent.exchangeToken {token} (public, mais le jeton EST le
//     secret à usage unique) → consomme le jeton ATOMIQUEMENT (une seule
//     requête peut gagner la course), renvoie la clé API déchiffrée.
//
// Un jeton déjà utilisé ou expiré est refusé (voir Exchange* error vars).

// AgentInstallToken est la vue "metadata" (sans la clé) d'un jeton
// d'installation, utile pour l'audit / le retour de createAgentInstallToken.
type AgentInstallToken struct {
	WorkspaceID  string    `json:"workspace_id"`
	APIKeyUserID string    `json:"api_key_user_id"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// AgentInstallCredentials est ce que l'échange d'un jeton valide renvoie au
// script d'installation : la clé API en clair (une seule fois), l'URL de
// l'API et l'identifiant du workspace.
type AgentInstallCredentials struct {
	APIKey      string `json:"api_key"`
	APIURL      string `json:"api_url"`
	WorkspaceID string `json:"workspace_id"`
}

// Erreurs dédiées : le handler HTTP les distingue pour renvoyer le bon code
// (404 inconnu, 410 déjà utilisé/expiré) sans fuiter lequel des deux au
// client (éviter l'oracle "jeton existe mais expiré" vs "jeton inexistant"
// serait sur-ingénierie ici : le jeton est à usage unique de toute façon,
// la distinction sert surtout l'UX de l'agent qui vient de rater la
// fenêtre de 10 minutes).
var (
	ErrAgentInstallTokenNotFound = errors.New("install token not found")
	ErrAgentInstallTokenUsed     = errors.New("install token already used")
	ErrAgentInstallTokenExpired  = errors.New("install token expired")
)

// AgentInstallTokenRecord est la row complète (hash + secret chiffré),
// manipulée uniquement par le repository et le service — jamais exposée
// telle quelle au handler HTTP.
type AgentInstallTokenRecord struct {
	TokenHash       string
	WorkspaceID     string
	APIKeyUserID    string
	EncryptedAPIKey string
	CreatedBy       string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	UsedAt          *time.Time
}

// AgentInstallTokenRepository persiste les jetons d'installation dans la
// base système (même DB que users/user_workspaces — pattern aligné sur
// VeridianAPIKeyGraceRepository).
type AgentInstallTokenRepository interface {
	// Insert enregistre un nouveau jeton. tokenHash est le sha256 hex du
	// jeton brut (jamais stocké en clair).
	Insert(ctx context.Context, rec *AgentInstallTokenRecord) error

	// ClaimByHash consomme ATOMIQUEMENT le jeton désigné par tokenHash :
	// seule une requête concurrente peut gagner la course (UPDATE ... WHERE
	// used_at IS NULL). Renvoie :
	//   - ErrAgentInstallTokenNotFound si aucune row ne porte ce hash
	//   - ErrAgentInstallTokenUsed si la row existe mais est déjà consommée
	//   - ErrAgentInstallTokenExpired si la row existe, n'est pas consommée,
	//     mais expires_at est dépassé
	//   - la row complète (avant consommation) sinon, avec used_at posé
	ClaimByHash(ctx context.Context, tokenHash string, now time.Time) (*AgentInstallTokenRecord, error)
}
