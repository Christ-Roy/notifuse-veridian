package domain

import (
	"context"
	"time"
)

// === Veridian patches ===
// Types et interfaces pour l'integration Hub Veridian.
// Voir veridian-platform/notifuse/README.md.

//go:generate mockgen -destination mocks/mock_veridian_plan_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianPlanRepository
//go:generate mockgen -destination mocks/mock_veridian_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianService
//go:generate mockgen -destination mocks/mock_webhook_emitter.go -package mocks github.com/Notifuse/notifuse/internal/domain WebhookEmitter

// PlanStatus represente l'etat d'un workspace cote Veridian.
type PlanStatus string

const (
	PlanStatusActive    PlanStatus = "active"
	PlanStatusSuspended PlanStatus = "suspended"
	PlanStatusDeleted   PlanStatus = "deleted" // soft delete, purge cron 30j
)

// VeridianPlan represente une ligne de la table veridian_plan.
type VeridianPlan struct {
	WorkspaceID            string     `json:"workspace_id"`
	Plan                   string     `json:"plan"` // free, pro, business, ...
	Status                 PlanStatus `json:"status"`
	MonthlyEmailQuota      int64      `json:"monthly_email_quota"` // -1 = unlimited
	EmailsSentThisMonth    int64      `json:"emails_sent_this_month"`
	LastResetAt            time.Time  `json:"last_reset_at"`
	SuspendedAt            *time.Time `json:"suspended_at,omitempty"`
	SuspendedReason        string     `json:"suspended_reason,omitempty"`
	DeletedAt              *time.Time `json:"deleted_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// QuotaRemaining retourne le nombre d'emails encore envoyables ce mois.
// -1 = illimite.
func (p *VeridianPlan) QuotaRemaining() int64 {
	if p.MonthlyEmailQuota < 0 {
		return -1
	}
	rem := p.MonthlyEmailQuota - p.EmailsSentThisMonth
	if rem < 0 {
		return 0
	}
	return rem
}

// IsBlocked retourne true si le workspace ne peut pas envoyer d'emails.
// Raisons possibles : suspended, deleted, quota mensuel atteint.
func (p *VeridianPlan) IsBlocked() (blocked bool, reason string) {
	if p.DeletedAt != nil {
		return true, "tenant deleted"
	}
	if p.Status == PlanStatusSuspended {
		if p.SuspendedReason != "" {
			return true, p.SuspendedReason
		}
		return true, "tenant suspended"
	}
	if p.MonthlyEmailQuota >= 0 && p.EmailsSentThisMonth >= p.MonthlyEmailQuota {
		return true, "monthly email quota exceeded"
	}
	return false, ""
}

// PlanQuotas mappe un nom de plan a un quota mensuel d'emails.
// Override possible via env var VERIDIAN_QUOTA_OVERRIDE (parse dans config).
var PlanQuotas = map[string]int64{
	"free":       500,
	"pro":        10000,
	"business":   50000,
	"enterprise": -1, // unlimited
}

// QuotaForPlan retourne le quota mensuel pour un plan donne.
// Si plan inconnu, retourne le quota free.
func QuotaForPlan(plan string) int64 {
	if q, ok := PlanQuotas[plan]; ok {
		return q
	}
	return PlanQuotas["free"]
}

// === Repository ===

// VeridianPlanRepository gere la persistence de la table veridian_plan.
type VeridianPlanRepository interface {
	Get(ctx context.Context, workspaceID string) (*VeridianPlan, error)
	Upsert(ctx context.Context, plan *VeridianPlan) error
	UpdatePlan(ctx context.Context, workspaceID, plan string, quota int64) error
	Suspend(ctx context.Context, workspaceID, reason string) error
	Resume(ctx context.Context, workspaceID string) error
	SoftDelete(ctx context.Context, workspaceID string) error
	IncrementEmailsSent(ctx context.Context, workspaceID string, delta int64) error
	ResetMonthlyCounters(ctx context.Context) (int64, error) // appele par cron
	// === Veridian patch === Hard delete (tests / admin platform).
	HardDelete(ctx context.Context, workspaceID string) error
	// ListByPrefix retourne tous les workspace_id matchant un prefix SQL LIKE.
	ListByPrefix(ctx context.Context, prefix string) ([]string, error)
}

// === Service ===

// ProvisionInput est le body de POST /api/tenants/provision.
type ProvisionInput struct {
	TenantID    string `json:"tenant_id"`    // workspace_id Notifuse
	OwnerEmail  string `json:"owner_email"`
	WorkspaceName string `json:"workspace_name,omitempty"` // optionnel, defaut = tenant_id
	Plan        string `json:"plan,omitempty"` // optionnel, defaut = VERIDIAN_DEFAULT_PLAN
}

// ProvisionResponse est la reponse de POST /api/tenants/provision.
type ProvisionResponse struct {
	WorkspaceID string `json:"workspace_id"`
	OwnerUserID string `json:"owner_user_id"`
	APIKey      string `json:"api_key"`
	APIKeyEmail string `json:"api_key_email"`
	// MagicLink : URL avec ?email=X&code=Y, demande saisie code par le user
	// (fallback si auto-login fail, ou pour les flows email natif).
	MagicLink string `json:"magic_link"`
	// AutoLoginURL : URL self-contained signee HMAC qui logge directement
	// le user owner dans la console Notifuse via localStorage. C'est l'URL
	// que le Hub utilise pour son bouton "Open Notifuse" — TTL 60s, le Hub
	// peut en regenerer via /api/workspaces.generateMagicLink quand le user
	// clique.
	AutoLoginURL string `json:"auto_login_url"`
	Plan         string `json:"plan"`
	Created      bool   `json:"created"` // false si idempotent (tenant existait deja)
}

// UpdatePlanInput est le body de POST /api/tenants/update-plan.
type UpdatePlanInput struct {
	TenantID string `json:"tenant_id"`
	Plan     string `json:"plan"`
}

// SuspendInput est le body de POST /api/tenants/suspend.
type SuspendInput struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

// ResumeInput est le body de POST /api/tenants/resume.
type ResumeInput struct {
	TenantID string `json:"tenant_id"`
}

// StatusResponse est la reponse de GET /api/tenants/:id/status.
type StatusResponse struct {
	TenantID            string     `json:"tenant_id"`
	Status              PlanStatus `json:"status"`
	Plan                string     `json:"plan"`
	MonthlyEmailQuota   int64      `json:"monthly_email_quota"`
	EmailsSentThisMonth int64      `json:"emails_sent_this_month"`
	QuotaRemaining      int64      `json:"quota_remaining"`
	SuspendedAt         *time.Time `json:"suspended_at,omitempty"`
	SuspendedReason     string     `json:"suspended_reason,omitempty"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
}

// MagicLinkInput est le body de POST /api/workspaces.generateMagicLink.
type MagicLinkInput struct {
	UserEmail string `json:"user_email"`
}

// MagicLinkResponse est la reponse de POST /api/workspaces.generateMagicLink.
// MagicLink : URL `/console/signin?email=X&code=Y` (fallback, demande saisie).
// AutoLoginURL : URL `/veridian/auto-login?token=<HMAC>` (auto-connect, TTL 60s).
type MagicLinkResponse struct {
	MagicLink    string    `json:"magic_link"`
	AutoLoginURL string    `json:"auto_login_url"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// TenantHealthResponse est la response de GET /api/tenants/{id}/health
// (livrable 3 du contrat README intégrations Hub Veridian).
//
// `magic_link_capable` est l'invariant clé : false si l'app ne peut pas
// générer un magic link self-contained pour cet owner (pas d'owner humain
// attaché, API key révoquée, tenant soft-deleted). Le Hub appelle ce
// endpoint en cron 1×/h pour détecter les régressions silencieuses du
// type bug 2026-05-17.
type TenantHealthResponse struct {
	TenantID         string     `json:"tenant_id"`
	WorkspaceID      string     `json:"workspace_id"`
	Status           PlanStatus `json:"status"`
	OwnerAttached    bool       `json:"owner_attached"`
	OwnerEmail       string     `json:"owner_email,omitempty"`
	OwnerUserID      string     `json:"owner_user_id,omitempty"`
	APIKeyValid      bool       `json:"api_key_valid"`
	MagicLinkCapable bool       `json:"magic_link_capable"`
	MembersCount     int        `json:"members_count"`
	Plan             string     `json:"plan,omitempty"`
	CheckedAt        time.Time  `json:"checked_at"`
}

// AttachOwnerInput est le body de POST /api/veridian/admin/attach-owner.
// Reservé à la reparation des tenants pre-existants (créés avant la feature
// Hub-Veridian) dont l'owner humain n'est pas attaché au workspace.
type AttachOwnerInput struct {
	TenantID   string `json:"tenant_id"`
	OwnerEmail string `json:"owner_email"`
}

// AttachOwnerResponse decrit l'état post-attach. Idempotent : `attached` est
// toujours true en sortie si l'op a réussi ; `already_attached` indique si la
// row user_workspaces existait déjà avant l'appel.
type AttachOwnerResponse struct {
	TenantID        string `json:"tenant_id"`
	OwnerEmail      string `json:"owner_email"`
	UserID          string `json:"user_id"`
	Attached        bool   `json:"attached"`
	AlreadyAttached bool   `json:"already_attached"`
	OwnerTransferred bool  `json:"owner_transferred"` // true si TransferOwnership a effectivement promu le user à owner pendant cet appel
}

// VeridianService est l'interface des operations Hub-driven.
type VeridianService interface {
	Provision(ctx context.Context, input ProvisionInput) (*ProvisionResponse, error)
	UpdatePlan(ctx context.Context, input UpdatePlanInput) error
	Suspend(ctx context.Context, input SuspendInput) error
	Resume(ctx context.Context, input ResumeInput) error
	SoftDelete(ctx context.Context, tenantID string) error
	GetStatus(ctx context.Context, tenantID string) (*StatusResponse, error)
	GenerateMagicLink(ctx context.Context, workspaceID, userEmail string) (*MagicLinkResponse, error)

	// === Veridian patch === Hard wipe pour cleanup CI / tests.
	// Detruit completement les tenants matchant un prefix (workspace + DB
	// postgres dediee + ligne veridian_plan + user owner). Pas de soft delete,
	// pas de fenetre 30j de purge. Reserve aux tests + admin platform.
	WipeTestTenants(ctx context.Context, input WipeTestTenantsInput) (*WipeTestTenantsResponse, error)

	// === Veridian patch === Repair endpoint pour tenants existants.
	// Trouve / crée le user humain owner_email, l'attache au workspace (role
	// member), puis promote owner (avec demotion de l'ancien owner non-humain).
	// Idempotent : appelable plusieurs fois sans effet de bord. Necessaire
	// pour reparer les workspaces créés avant le flow Hub-driven dont l'owner
	// humain n'a jamais été attaché.
	AttachOwner(ctx context.Context, input AttachOwnerInput) (*AttachOwnerResponse, error)

	// === Veridian patch === Health observable du tenant (livrable 3 contrat
	// intégrations Hub). Renvoie l'état réel cote app : owner humain attaché,
	// API key valide, capacité magic link, count membres. Le Hub poll en cron
	// 1×/h pour détecter régressions du type bug 2026-05-17 (owner orphelin).
	Health(ctx context.Context, tenantID string) (*TenantHealthResponse, error)
}

// EventTenantOwnerChanged event émis quand AttachOwner promote un user humain
// à owner avec demote/remove de l'ancien owner.
const EventTenantOwnerChanged VeridianEvent = "tenant.owner_changed"

// WipeTestTenantsInput est le body de POST /api/veridian/admin/wipe-test-tenants.
// Soit un prefix (`prefix: "e2e-"`) soit une liste explicite (`tenant_ids: [...]`).
// Le caller doit fournir au moins un des deux.
type WipeTestTenantsInput struct {
	Prefix    string   `json:"prefix,omitempty"`     // ex: "e2e-", "chaos5", ...
	TenantIDs []string `json:"tenant_ids,omitempty"` // exhaustif (alternative au prefix)
	// SafetyClientPrefixes : prefixes de tenants a NE JAMAIS supprimer
	// (clients reels staging). Si vide, defaut : apicalinfo, robinix, lyon,
	// loyer, veridiansite. Necessaire pour proteger les tenants prod en cas
	// de fuite du HUB_API_SECRET vers un attaquant.
	SafetyClientPrefixes []string `json:"safety_client_prefixes,omitempty"`
}

// WipeTestTenantsResponse renvoie la liste des tenants supprimes + erreurs.
type WipeTestTenantsResponse struct {
	Wiped   []string          `json:"wiped"`             // tenant_ids supprimes avec succes
	Skipped []string          `json:"skipped,omitempty"` // tenant_ids matchant safety prefixes
	Errors  map[string]string `json:"errors,omitempty"`  // tenant_id → message d'erreur
}

// === Webhook events vers Hub ===

// VeridianEvent represente un event push vers le Hub.
type VeridianEvent string

const (
	EventTenantProvisioned VeridianEvent = "tenant.provisioned"
	EventTenantSuspended   VeridianEvent = "tenant.suspended"
	EventTenantResumed     VeridianEvent = "tenant.resumed"
	EventTenantDeleted     VeridianEvent = "tenant.deleted"
	EventTenantPlanChanged VeridianEvent = "tenant.plan_changed"
	EventEmailSent         VeridianEvent = "email.sent"
	EventEmailBounced      VeridianEvent = "email.bounced"
	EventEmailComplaint    VeridianEvent = "email.complaint"
	EventQuotaExceeded     VeridianEvent = "tenant.quota_exceeded"
)

// VeridianEventPayload est le payload signe envoye au Hub.
type VeridianEventPayload struct {
	EventID     string                 `json:"event_id"` // UUID, pour idempotence cote Hub
	EventType   VeridianEvent          `json:"event_type"`
	TenantID    string                 `json:"tenant_id"`
	OccurredAt  time.Time              `json:"occurred_at"`
	Data        map[string]interface{} `json:"data,omitempty"`
}

// WebhookEmitter envoie des events au Hub.
type WebhookEmitter interface {
	Emit(ctx context.Context, eventType VeridianEvent, tenantID string, data map[string]interface{})
}
