package domain

//go:generate mockgen -destination mocks/mock_veridian_api_key_grace_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianAPIKeyGraceRepository

// === Veridian patch — Lot K (2026-05-21) ===
// Types pour les endpoints CONTRAT-HUB §5.15 (rotate-api-key) et §5.16
// (transfer-owner). Ticket todo/2026-05-19-rotate-transfer-owner-endpoints.md.
//
// Conventions :
//   - Input : body JSON décodé par le handler, TenantID injecté depuis le
//     path param par le handler (pas dans le body).
//   - Response : strictement le format attendu par le contrat Hub.
//   - reason : champ obligatoire pour audit GDPR. Validation côté handler
//     (400 si vide).

import (
	"context"
	"time"
)

// === §5.15 — rotate-api-key ===

// RotateAPIKeyInput est le body de POST /api/tenants/{id}/rotate-api-key.
// reason obligatoire pour audit GDPR (consigne SOC2 + traçabilité ops Hub).
type RotateAPIKeyInput struct {
	// TenantID est injecte depuis le path param par le handler.
	TenantID string `json:"-"`
	// Reason : motif de la rotation (ex: "scheduled rotation 90j", "leak
	// suspected", "client request"). Audite via emitter webhook payload.
	Reason string `json:"reason"`
}

// RotateAPIKeyResponse decrit la nouvelle api_key + la fin de grace period
// pour l'ancienne. Le caller (Hub) DOIT remplacer son secret immediatement
// puis utiliser exclusivement la nouvelle avant OldAPIKeyRevokesAt — apres,
// l'ancienne renvoie 401.
//
// CONTRAT-HUB §5.15 : { tenant_id, new_api_key, new_api_key_email, old_api_key_revokes_at }.
type RotateAPIKeyResponse struct {
	TenantID            string    `json:"tenant_id"`
	NewAPIKey           string    `json:"new_api_key"`
	NewAPIKeyEmail      string    `json:"new_api_key_email"`
	OldAPIKeyRevokesAt  time.Time `json:"old_api_key_revokes_at"`
}

// APIKeyGracePeriod est la duree pendant laquelle l'ancienne api_key reste
// valide apres rotation. CONTRAT-HUB §5.15 : 5 minutes. Constante exportee
// pour permettre aux tests de mocker un grace court (ex: 1ms en unit-test
// pour valider le check d'expiration sans attendre).
const APIKeyGracePeriod = 5 * time.Minute

// EventTenantAPIKeyRotated est emis quand RotateAPIKey reussit. Le payload
// inclut new_api_key_email (NE PAS logger la key elle-meme) + revoke_at +
// reason. Le Hub consomme pour mettre a jour son secret store (KMS, Vault).
const EventTenantAPIKeyRotated VeridianEvent = "tenant.api_key_rotated"

// === §5.16 — transfer-owner ===

// TransferOwnerInput est le body de POST /api/tenants/{id}/transfer-owner.
// new_owner_email est l'email du futur owner. reason obligatoire (audit).
type TransferOwnerInput struct {
	// TenantID est injecte depuis le path param par le handler.
	TenantID string `json:"-"`
	// NewOwnerEmail : email du futur owner du tenant.
	NewOwnerEmail string `json:"new_owner_email"`
	// Reason : motif du transfer (ex: "client account migration", "support
	// request"). Audite via emitter webhook payload.
	Reason string `json:"reason"`
}

// TransferOwnerResponse decrit le transfer effectue.
//
// CONTRAT-HUB §5.16 : { tenant_id, old_owner, new_owner, transferred_at }.
//
// Note divergence Notifuse : le contrat dit "l'ancien owner devient admin",
// mais Notifuse upstream n'a PAS de role admin natif (seulement owner/member).
// L'ancien owner est demote en member (cf. AttachOwner). Cosmetique :
// fonctionnellement il a perdu les droits owner — l'intention contrat est
// respectee.
type TransferOwnerResponse struct {
	TenantID      string    `json:"tenant_id"`
	OldOwner      string    `json:"old_owner"`
	NewOwner      string    `json:"new_owner"`
	TransferredAt time.Time `json:"transferred_at"`
}

// === Repository — api_key_grace ===

// APIKeyGraceEntry represente une row dans veridian_api_key_grace : un user
// api_key marque pour revocation a revoke_at. Le cron veridian_api_key_grace_
// cleanup scan les rows expirees, DELETE le user upstream, puis DELETE la row.
type APIKeyGraceEntry struct {
	APIKeyUserID string    `json:"api_key_user_id"`
	WorkspaceID  string    `json:"workspace_id"`
	RevokeAt     time.Time `json:"revoke_at"`
	Reason       string    `json:"reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// VeridianAPIKeyGraceRepository expose les operations CRUD pour la table
// veridian_api_key_grace. Insert idempotent (UPSERT) pour les re-rotates,
// ListExpired pour le cron, DeleteByID pour cleanup post-revocation.
type VeridianAPIKeyGraceRepository interface {
	// Insert ajoute (ou met a jour) une entree de grace. Idempotent : un
	// re-rotate du meme user api_key ecrase revoke_at et reason — utile si
	// l'agent re-rotate dans la grace period precedente.
	Insert(ctx context.Context, entry *APIKeyGraceEntry) error
	// ListExpired retourne les entrees dont revoke_at <= now. Le cron pop
	// cette liste, DELETE les users upstream, puis DELETE les rows par ID.
	ListExpired(ctx context.Context, now time.Time) ([]*APIKeyGraceEntry, error)
	// DeleteByID supprime une entree (apres revocation effective du user).
	DeleteByID(ctx context.Context, apiKeyUserID string) error
}
