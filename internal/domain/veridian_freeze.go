package domain

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
//
// Types pour les endpoints :
//
//	POST /api/tenants/{tenantId}/freeze-member
//	POST /api/tenants/{tenantId}/unfreeze-member
//
// Contexte business : quand un user depasse son quota seat (5 Pro, 25 Business),
// le Hub envoie un freeze sur les derniers invites → mode degrade lecture seule
// + 402 user_frozen sur ecritures. Le user reste membre du workspace (pas de
// remove), reversible via unfreeze. Permet d'enforcer le quota sans casser les
// workflows ni virer des membres.
//
// Storage : table dediee veridian_frozen_members (workspace_id, user_id,
// frozen_at, reason). Migration V47. Pas de patch upstream sur user_workspaces.

import (
	"context"
	"time"
)

// FreezeReason qualifie pourquoi un user a ete frozen. Tracee pour audit +
// affichage UI cote console (banniere "Account frozen: <reason>").
type FreezeReason string

const (
	// FreezeReasonQuotaSeatExceeded : Hub a depasse son quota seats sur ce
	// tenant et a frozen les derniers invites (§5.21).
	FreezeReasonQuotaSeatExceeded FreezeReason = "quota_seat_exceeded"
	// FreezeReasonManual : admin Hub a frozen manuellement (ex: abuse,
	// review en cours).
	FreezeReasonManual FreezeReason = "manual"
)

// IsValid retourne true si la reason est connue. Defensif : on accepte une
// reason vide cote handler (fallback "manual") plutot que de rejeter.
func (r FreezeReason) IsValid() bool {
	switch r {
	case FreezeReasonQuotaSeatExceeded, FreezeReasonManual:
		return true
	default:
		return false
	}
}

// FreezeMemberInput est le body de POST /api/tenants/{tenantId}/freeze-member.
// Appele par le Hub apres detection seat overage soft warning (§5.21).
type FreezeMemberInput struct {
	UserEmail string       `json:"user_email"`
	HubUserID string       `json:"hub_user_id"`
	Reason    FreezeReason `json:"reason,omitempty"`
	// TenantID : injecte par le handler depuis le path param {tenantId}.
	TenantID string `json:"-"`
}

// FreezeMemberResponse est la reponse 200 de freeze-member.
type FreezeMemberResponse struct {
	TenantID  string       `json:"tenant_id"`
	UserEmail string       `json:"user_email"`
	HubUserID string       `json:"hub_user_id"`
	FrozenAt  time.Time    `json:"frozen_at"`
	Reason    FreezeReason `json:"reason"`
}

// UnfreezeMemberInput est le body de POST /api/tenants/{tenantId}/unfreeze-member.
type UnfreezeMemberInput struct {
	UserEmail string `json:"user_email"`
	HubUserID string `json:"hub_user_id"`
	// TenantID : injecte par le handler depuis le path param {tenantId}.
	TenantID string `json:"-"`
}

// UnfreezeMemberResponse est la reponse 200 de unfreeze-member.
type UnfreezeMemberResponse struct {
	TenantID   string    `json:"tenant_id"`
	UserEmail  string    `json:"user_email"`
	HubUserID  string    `json:"hub_user_id"`
	UnfrozenAt time.Time `json:"unfrozen_at"`
}

// VeridianFrozenMember represente une ligne de la table veridian_frozen_members
// (migration V47). Utilise par le middleware paywall per-user pour decider si
// une requete doit etre bloquee/obfusquee.
type VeridianFrozenMember struct {
	WorkspaceID string       `json:"workspace_id" db:"workspace_id"`
	UserID      string       `json:"user_id" db:"user_id"`
	FrozenAt    time.Time    `json:"frozen_at" db:"frozen_at"`
	Reason      FreezeReason `json:"reason" db:"reason"`
}

// VeridianFrozenMemberRepository est l'interface d'acces a la table
// veridian_frozen_members. Le middleware paywall + le service freeze/unfreeze
// l'utilisent. Implementation Postgres dans internal/repository.
type VeridianFrozenMemberRepository interface {
	// Freeze insere ou met a jour le freeze d'un user dans un workspace.
	// Idempotent : si la row existe deja avec un frozen_at non-nul, on garde
	// le frozen_at original (pas de UPDATE), on retourne juste la ligne.
	// Si la row n'existe pas, INSERT avec frozen_at = NOW().
	//
	// Retourne (frozen_member, alreadyFrozen, error). alreadyFrozen=true
	// permet au handler de renvoyer 409 ou 200 idempotent au choix.
	Freeze(ctx context.Context, workspaceID, userID string, reason FreezeReason) (*VeridianFrozenMember, bool, error)

	// Unfreeze supprime la row freeze d'un user dans un workspace.
	// Idempotent : si la row n'existe pas, retourne (false, nil) sans erreur.
	// Retourne (wasFrozen, error).
	Unfreeze(ctx context.Context, workspaceID, userID string) (bool, error)

	// IsFrozen retourne true si une row freeze existe pour ce (workspace, user).
	// Optimise pour le hot path du middleware paywall. Retourne (frozen, reason, error).
	// Si pas frozen : (false, "", nil).
	IsFrozen(ctx context.Context, workspaceID, userID string) (bool, FreezeReason, error)
}

// === Webhook events ===
//
// Le service emit ces events vers le Hub apres freeze/unfreeze reussi
// (best-effort, pattern emitter existant — cf. veridian_membership_service.go).
const (
	// EventTenantMemberFrozen est emis apres freeze reussi (nouveau).
	// Payload : {user_email, hub_user_id, app_user_id, reason, frozen_at, actor}
	EventTenantMemberFrozen VeridianEvent = "tenant.member_frozen"

	// EventTenantMemberUnfrozen est emis apres unfreeze reussi (etait frozen).
	// Payload : {user_email, hub_user_id, app_user_id, unfrozen_at, actor}
	EventTenantMemberUnfrozen VeridianEvent = "tenant.member_unfrozen"
)
