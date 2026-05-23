package domain

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
//
// Types pour les endpoints CONTRAT-HUB §5.18 (sync-member), §5.19 (remove-member),
// §5.20 (restore-member). Implementation Option C : le Hub stocke
// `hub_app.tenant_members`, propage les membres aux apps downstream via webhook
// HMAC, l'app gere ses roles internes.
//
// Voir todo/2026-05-19-v13-multi-membre-cross-app.md.
//
// Endpoints exposes cote Notifuse :
//
//	POST /api/tenants/{id}/sync-member     -> SyncMemberResponse
//	POST /api/tenants/{id}/remove-member   -> RemoveMemberResponse
//	POST /api/tenants/{id}/restore-member  -> RestoreMemberResponse
//
// NOTE freeze/unfreeze (§5.21) : non livre dans la session du 2026-05-23 — le
// paywall middleware Notifuse est tenant-level, pas per-user. Le freeze
// per-membre exige une refonte (nouvelle colonne user_workspaces.frozen_at +
// integration middleware) qui depasse le scope minimal v1.3. Ticket de suivi
// a creer si Robert souhaite l'activer une fois le quota seats live cote Hub.

import "time"

// === sync-member (§5.18.3) =================================================

// SyncMemberInput est le body de POST /api/tenants/{id}/sync-member.
// Appele par le Hub apres invitation/acceptation cross-app pour propager le
// nouveau membre a l'app downstream.
//
// Comportement obligatoire (cf §5.18.3) :
//   1. Create user app local s'il n'existe pas (type=user, sans password).
//   2. Ajoute le user au workspace tenant (role member par defaut, additif).
//   3. Idempotent : si user deja membre, retourne 200 sans rien changer.
//   4. JAMAIS retirer un user existant (additif uniquement). Pour retrait,
//      utiliser §5.19 remove-member.
type SyncMemberInput struct {
	// UserEmail : source de verite identite cross-app (CONTRAT-HUB §3.7).
	UserEmail string `json:"user_email"`
	// HubUserID : id stable cote hub_app.users. Conserve pour audit uniquement —
	// Notifuse resout l'identite par EMAIL, pas par id Hub.
	HubUserID string `json:"hub_user_id"`
	// Role : default role envoye par le Hub (member|admin). L'app peut le
	// surcharger en interne — le Hub n'est PAS autoritatif sur les roles app.
	Role SyncMemberRole `json:"role"`
	// InvitedAt : timestamp invitation (Hub→app, audit uniquement).
	InvitedAt time.Time `json:"invited_at,omitempty"`
	// JoinedAt : timestamp acceptation (Hub→app, audit uniquement).
	JoinedAt time.Time `json:"joined_at,omitempty"`
	// TenantID : injecte par le handler depuis le path param {id}.
	TenantID string `json:"-"`
}

// SyncMemberRole represente les roles acceptes par sync-member. Pour Notifuse,
// tous les invites = `member` en interne (workspace upstream n'a que owner/member),
// mais on accepte les 2 valeurs CONTRAT-HUB pour back-compat.
type SyncMemberRole string

const (
	SyncMemberRoleMember SyncMemberRole = "member"
	SyncMemberRoleAdmin  SyncMemberRole = "admin"
)

// IsValid retourne true si le role est member ou admin (les 2 valeurs CONTRAT-HUB
// pour sync-member). owner est EXCLU — un owner ne s'invite pas via sync-member
// (il existe via provision ou transfer-owner).
func (r SyncMemberRole) IsValid() bool {
	switch r {
	case SyncMemberRoleMember, SyncMemberRoleAdmin:
		return true
	default:
		return false
	}
}

// SyncMemberResponse est la reponse 200 de POST /api/tenants/{id}/sync-member.
// Format CONTRAT-HUB §5.18.3.
type SyncMemberResponse struct {
	TenantID  string `json:"tenant_id"`
	UserEmail string `json:"user_email"`
	Synced    bool   `json:"synced"`
	// AppUserID : id user Notifuse (UUID natif, peut differer de hub_user_id).
	AppUserID string `json:"app_user_id"`
	// AppRole : role effectif Notifuse apres l'op. Pour Notifuse, toujours
	// "member" ou "owner" (workspace upstream n'a pas de role admin). Si le
	// user etait deja owner, on garde "owner" (additif, pas de downgrade).
	AppRole string `json:"app_role"`
}

// === remove-member (§5.19.2) ================================================

// RemoveMemberInput est le body de POST /api/tenants/{id}/remove-member.
// Appele par le Hub quand l'admin retire un user du tenant. Hard delete cote
// app (le user reste en table users pour audit + restauration eventuelle).
//
// Garde-fou : impossible de retirer le owner du workspace → 409 cannot_remove_owner.
// Pour transferer l'ownership, utiliser §5.16 (transfer-owner).
type RemoveMemberInput struct {
	UserEmail string `json:"user_email"`
	// Reason : "user_request" | "admin_action" (audit GDPR).
	Reason string `json:"reason,omitempty"`
	// TenantID : injecte par le handler depuis le path param {id}.
	TenantID string `json:"-"`
}

// RemoveMemberResponse est la reponse 200 de POST /api/tenants/{id}/remove-member.
type RemoveMemberResponse struct {
	TenantID  string    `json:"tenant_id"`
	UserEmail string    `json:"user_email"`
	RemovedAt time.Time `json:"removed_at"`
}

// === restore-member (§5.20) =================================================

// RestoreMemberInput est le body de POST /api/tenants/{id}/restore-member.
// Annule un remove-member precedent. Idempotent : re-call sans effet si le
// user est deja membre actif.
type RestoreMemberInput struct {
	UserEmail string `json:"user_email"`
	// TenantID : injecte par le handler depuis le path param {id}.
	TenantID string `json:"-"`
}

// RestoreMemberResponse est la reponse 200 de POST /api/tenants/{id}/restore-member.
type RestoreMemberResponse struct {
	TenantID   string    `json:"tenant_id"`
	UserEmail  string    `json:"user_email"`
	RestoredAt time.Time `json:"restored_at"`
}
