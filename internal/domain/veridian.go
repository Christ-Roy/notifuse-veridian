package domain

import (
	"context"
	"encoding/json"
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

// PlanSource represente l'origine d'un plan tenant (CONTRAT-HUB sec. 3.3).
// Distingue les plans Stripe (mutables par webhook) des plans offerts
// (immunes aux downgrades automatiques).
type PlanSource string

const (
	// PlanSourceStripe : plan paye, source de verite = Stripe (Hub webhooks).
	// C'est le defaut pour back-compat des appels Hub legacy.
	PlanSourceStripe PlanSource = "stripe"
	// PlanSourceManual : assigne par Robert manuellement, peut etre annule.
	PlanSourceManual PlanSource = "manual"
	// PlanSourceLifetimeSiteVitrine : offert via le site vitrine, immune.
	PlanSourceLifetimeSiteVitrine PlanSource = "lifetime_site_vitrine"
	// PlanSourceLifetimePartner : offert a un partenaire, immune.
	PlanSourceLifetimePartner PlanSource = "lifetime_partner"
	// PlanSourceInternal : usage interne Veridian, immune.
	PlanSourceInternal PlanSource = "internal"
)

// IsImmune renvoie true si le plan_source est immunise contre les
// downgrades automatiques venant du Hub (Stripe webhook). Si l'appelant
// Hub envoie plan_source=stripe vers un plan IsImmune, on refuse avec
// ErrPlanImmune → 409 plan_locked.
func (s PlanSource) IsImmune() bool {
	switch s {
	case PlanSourceLifetimeSiteVitrine, PlanSourceLifetimePartner, PlanSourceInternal, PlanSourceManual:
		return true
	default:
		return false
	}
}

// IsValid renvoie true si la valeur correspond a une des sources connues.
// Une chaine vide est consideree valide (= default 'stripe' au upsert).
func (s PlanSource) IsValid() bool {
	switch s {
	case "", PlanSourceStripe, PlanSourceManual, PlanSourceLifetimeSiteVitrine, PlanSourceLifetimePartner, PlanSourceInternal:
		return true
	default:
		return false
	}
}

// VeridianPlan represente une ligne de la table veridian_plan.
type VeridianPlan struct {
	WorkspaceID         string     `json:"workspace_id"`
	Plan                string     `json:"plan"`        // free, pro, business, ...
	PlanSource          PlanSource `json:"plan_source"` // stripe/manual/lifetime_*/internal — cf. CONTRAT-HUB sec. 3.3
	Status              PlanStatus `json:"status"`
	MonthlyEmailQuota   int64      `json:"monthly_email_quota"` // -1 = unlimited
	EmailsSentThisMonth int64      `json:"emails_sent_this_month"`
	LastResetAt         time.Time  `json:"last_reset_at"`
	SuspendedAt         *time.Time `json:"suspended_at,omitempty"`
	SuspendedReason     string     `json:"suspended_reason,omitempty"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
	// === Lifecycle (CONTRAT-HUB sec. 5.7 + 5.8) ===
	// RestoredAt : timestamp du dernier Restore. Audit trail uniquement, n'a
	// pas d'effet fonctionnel apres restore (deleted_at est clear, le tenant
	// est de nouveau actif).
	RestoredAt *time.Time `json:"restored_at,omitempty"`
	// PurgeEligibleAt : timestamp a partir duquel le tenant peut etre
	// hard-deleted. Set a NOW + veridianPurgeDelay au SoftDelete, NULL apres
	// Restore. Le service.Purge refuse si NOW < PurgeEligibleAt.
	PurgeEligibleAt *time.Time `json:"purge_eligible_at,omitempty"`
	// LastTouchedAt : timestamp du dernier Touch (heartbeat anti-soft-delete
	// par cron). Debounced cote service (24h).
	LastTouchedAt *time.Time `json:"last_touched_at,omitempty"`
	// LifecycleReason : derniere raison appliquee au lifecycle (audit GDPR
	// + traceabilite). Ecrasee a chaque transition (soft-delete, restore,
	// purge).
	LifecycleReason string    `json:"lifecycle_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// === Pricing dimensions (V37, ticket pricing-plans-implementation) ===
	// Convention -1 = illimite (semantique partagee avec MonthlyEmailQuota).
	// Ces colonnes sont initialement persistees par la migration V37 avec
	// les defaults Free + backfill par plan. Le repository (lot 2) lit /
	// ecrit ces champs. Pour ce lot 1 (domain), elles existent comme
	// surface contractuelle stable que repo + service + middleware vont
	// progressivement consommer. Cf. DefaultPlanLimits + LimitsForPlan.
	MaxContacts            int64 `json:"max_contacts"`
	MaxSeats               int   `json:"max_seats"`
	MaxOAuthAccounts       int   `json:"max_oauth_accounts"`
	MaxCustomDomains       int   `json:"max_custom_domains"`
	MaxActiveSequences     int   `json:"max_active_sequences"`
	FeatureABTesting       bool  `json:"feature_ab_testing"`
	FeatureBrandingRemoved bool  `json:"feature_branding_removed"`
	FeatureWhiteLabel      bool  `json:"feature_white_label"`
	HistoryRetentionDays   int   `json:"history_retention_days"`
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
// Raisons possibles : suspended, deleted.
//
// === DÉCISION 2026-05-20 ===
// Le quota mensuel d'envoi (monthly_email_quota) N'EST PLUS un motif de
// blocage. Raison business : Notifuse ne fournit AUCUN provider d'envoi
// — les clients connectent leur propre Gmail/Outlook/SES/SMTP (BYO).
// C'est leur provider qui a les limites d'envoi, pas nous. Limiter ici
// serait du paywall artificiel sur un service qu'on n'offre pas.
//
// On continue d'incrémenter emails_sent_this_month via le decorator pour
// usage statistique + audit + futur cas Phase C (provider managé Veridian
// avec Resend) — mais aucun blocage runtime tant qu'on ne fournit pas
// nous-mêmes le sending. Cf. memory project_email_sending_strategy.
//
// Suspend et deleted continuent de bloquer (légitimes : suspended =
// problème compte Veridian, deleted = compte fermé).
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
	// Volontairement PAS de check sur monthly_email_quota — voir doc ci-dessus.
	return false, ""
}

// PlanQuotas mappe un nom de plan a un quota mensuel d'emails.
//
// === DÉCISION 2026-05-20 === Tous les plans = -1 (illimité) tant que
// Veridian ne fournit pas son propre provider d'envoi. Le client utilise
// sa propre boîte (BYO) → c'est son provider qui limite, pas nous.
// IsBlocked() ne check plus ce quota. Le compteur emails_sent_this_month
// continue d'etre incremente pour stats/audit/futur Resend managé.
//
// Si Phase C (Veridian managed sending avec Resend) arrive un jour, on
// reactivera ces seuils + le check dans IsBlocked.
var PlanQuotas = map[string]int64{
	"free":       -1, // unlimited (BYO sending)
	"pro":        -1, // unlimited (BYO sending)
	"business":   -1, // unlimited (BYO sending)
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

// PlanLimits regroupe TOUTES les dimensions d'un plan Veridian
// (cf. VISION-BUSINESS.md + ticket todo/2026-05-20-pricing-plans-implementation).
// Convention -1 = illimite (semantique partagee avec MonthlyEmailQuota).
//
// MonthlyEmailQuota reste a -1 pour tous les plans tant que Veridian ne
// fournit pas son propre provider d'envoi (cf. memory
// project_email_sending_strategy). Le champ est conserve pour Phase C
// (Resend manage) sans casser la surface contractuelle.
type PlanLimits struct {
	MonthlyEmailQuota      int64
	MaxContacts            int64
	MaxSeats               int
	MaxOAuthAccounts       int
	MaxCustomDomains       int
	MaxActiveSequences     int
	FeatureABTesting       bool
	FeatureBrandingRemoved bool
	FeatureWhiteLabel      bool
	HistoryRetentionDays   int
}

// DefaultPlanLimits expose les limites par defaut de chaque plan, en miroir
// du backfill V37 + de VISION-BUSINESS.md. Le repository utilise cette map
// au provision / update-plan pour appliquer les limites en l'absence de
// custom override (champ `quotas` envoye par le Hub).
var DefaultPlanLimits = map[string]PlanLimits{
	"free": {
		MonthlyEmailQuota:      -1, // BYO sending, pas de cap Notifuse
		MaxContacts:            500,
		MaxSeats:               1,
		MaxOAuthAccounts:       1,
		MaxCustomDomains:       0,
		MaxActiveSequences:     1,
		FeatureABTesting:       false,
		FeatureBrandingRemoved: false,
		FeatureWhiteLabel:      false,
		HistoryRetentionDays:   30,
	},
	"pro": {
		MonthlyEmailQuota:      -1,
		MaxContacts:            5000,
		MaxSeats:               5,
		MaxOAuthAccounts:       5,
		MaxCustomDomains:       1,
		MaxActiveSequences:     -1,
		FeatureABTesting:       true,
		FeatureBrandingRemoved: true,
		FeatureWhiteLabel:      false,
		HistoryRetentionDays:   365,
	},
	"business": {
		MonthlyEmailQuota:      -1,
		MaxContacts:            25000,
		MaxSeats:               25,
		MaxOAuthAccounts:       25,
		MaxCustomDomains:       5,
		MaxActiveSequences:     -1,
		FeatureABTesting:       true,
		FeatureBrandingRemoved: true,
		FeatureWhiteLabel:      true,
		HistoryRetentionDays:   -1,
	},
	"enterprise": {
		MonthlyEmailQuota:      -1,
		MaxContacts:            -1,
		MaxSeats:               -1,
		MaxOAuthAccounts:       -1,
		MaxCustomDomains:       -1,
		MaxActiveSequences:     -1,
		FeatureABTesting:       true,
		FeatureBrandingRemoved: true,
		FeatureWhiteLabel:      true,
		HistoryRetentionDays:   -1,
	},
}

// LimitsForPlan retourne les limites par defaut pour un plan donne.
// Si plan inconnu (ou chaine vide), fallback Free — semantique safe :
// le tenant ne pourra rien faire de plus que Free, jamais d'escalade
// silencieuse de privileges.
func LimitsForPlan(plan string) PlanLimits {
	if l, ok := DefaultPlanLimits[plan]; ok {
		return l
	}
	return DefaultPlanLimits["free"]
}

// === Repository ===

// VeridianPlanRepository gere la persistence de la table veridian_plan.
type VeridianPlanRepository interface {
	Get(ctx context.Context, workspaceID string) (*VeridianPlan, error)
	Upsert(ctx context.Context, plan *VeridianPlan) error
	// UpdatePlan modifie plan + quota + plan_source d'un workspace existant.
	// Si planSource est vide, la valeur DB existante est preservee (pas
	// d'ecrasement implicite par "stripe"). Le service appelle cette methode
	// uniquement apres avoir verifie l'immunite (cf. ErrPlanImmune).
	UpdatePlan(ctx context.Context, workspaceID, plan string, quota int64, planSource PlanSource) error
	Suspend(ctx context.Context, workspaceID, reason string) error
	Resume(ctx context.Context, workspaceID string) error
	// SoftDelete marque le tenant deleted_at = NOW + initialise
	// purge_eligible_at = NOW + 30j + lifecycle_reason (audit GDPR).
	// CONTRAT-HUB sec. 5.7-5.8.
	SoftDelete(ctx context.Context, workspaceID, reason string) error
	// Restore annule un soft-delete : clear deleted_at + purge_eligible_at,
	// set restored_at = NOW + lifecycle_reason. Le tenant repasse en active.
	Restore(ctx context.Context, workspaceID, reason string) error
	// Purge supprime DEFINITIVEMENT la ligne veridian_plan (hard delete).
	// Le caller (service) doit avoir verifie purge_eligible_at &lt; NOW avant.
	// La suppression de la DB workspace et du user owner est faite en amont
	// par WorkspaceService.DeleteWorkspace.
	Purge(ctx context.Context, workspaceID, reason string) error
	// Touch met a jour last_touched_at = NOW (heartbeat anti-soft-delete
	// par cron Hub). Pas d'autre effet de bord — le service applique le
	// debouncing 24h en amont.
	Touch(ctx context.Context, workspaceID string) error
	IncrementEmailsSent(ctx context.Context, workspaceID string, delta int64) error
	ResetMonthlyCounters(ctx context.Context) (int64, error) // appele par cron
	// === Veridian patch === Hard delete (tests / admin platform).
	HardDelete(ctx context.Context, workspaceID string) error
	// ListByPrefix retourne tous les workspace_id matchant un prefix SQL LIKE.
	ListByPrefix(ctx context.Context, prefix string) ([]string, error)
}

// === Service ===

// PlanQuotasInput regroupe les quotas configurables par tenant envoyes par
// le Hub au moment du provision / update-plan (CONTRAT-HUB sec. 5.17).
// Permet au Hub de centraliser la source de verite des quotas plutot que
// de dependre du hardcoded QuotaForPlan(plan) cote Notifuse.
//
// Tous les champs sont des pointeurs pour distinguer "non envoye" (nil =
// utiliser le hardcoded) de "envoye explicitement avec 0" (limite zero). La
// valeur -1 represente l'illimite (semantique partagee avec
// VeridianPlan.MonthlyEmailQuota).
//
// Pour l'instant un seul quota (monthly_emails). Le struct est extensible
// pour les futurs quotas contrats sec. 5.17 (contacts, broadcasts, templates,
// storage_mb, ...).
//
// A ne pas confondre avec la variable globale `PlanQuotas` (map plan→quota
// par defaut hardcoded) qui sert de fallback quand l'appelant n'envoie pas
// le champ.
type PlanQuotasInput struct {
	MonthlyEmails *int64 `json:"monthly_emails,omitempty"`
}

// ProvisionInput est le body de POST /api/tenants/provision.
type ProvisionInput struct {
	TenantID      string      `json:"tenant_id"` // workspace_id Notifuse
	OwnerEmail    string      `json:"owner_email"`
	WorkspaceName string      `json:"workspace_name,omitempty"` // optionnel, defaut = tenant_id
	Plan          string      `json:"plan,omitempty"`           // optionnel, defaut = VERIDIAN_DEFAULT_PLAN
	PlanSource    PlanSource  `json:"plan_source,omitempty"`    // optionnel, defaut = "stripe"
	Quotas        *PlanQuotasInput `json:"quotas,omitempty"`    // optionnel, defaut = QuotaForPlan(plan)
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
	TenantID   string      `json:"tenant_id"`
	Plan       string      `json:"plan"`
	PlanSource PlanSource  `json:"plan_source,omitempty"` // optionnel, defaut = "stripe"
	Quotas     *PlanQuotasInput `json:"quotas,omitempty"` // optionnel, defaut = QuotaForPlan(plan)
}

// UpdatePlanResponse est la reponse de POST /api/tenants/update-plan
// (CONTRAT-HUB sec. 5.2 : audit trail previous_plan).
type UpdatePlanResponse struct {
	TenantID     string     `json:"tenant_id"`
	Plan         string     `json:"plan"`
	PreviousPlan string     `json:"previous_plan,omitempty"`
	PlanSource   PlanSource `json:"plan_source"`
	AppliedAt    time.Time  `json:"applied_at"`
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

// === Lifecycle (CONTRAT-HUB sec. 5.7-5.8) ===

// SoftDeleteInput est le body de POST /api/tenants/{id}/soft-delete.
// reason vide est tolere (back-compat handler DELETE legacy).
type SoftDeleteInput struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

// SoftDeleteResponse — audit trail post-soft-delete.
type SoftDeleteResponse struct {
	TenantID        string    `json:"tenant_id"`
	Status          string    `json:"status"` // "soft_deleted"
	DeletedAt       time.Time `json:"deleted_at"`
	PurgeEligibleAt time.Time `json:"purge_eligible_at"`
}

// RestoreInput est le body de POST /api/tenants/{id}/restore.
type RestoreInput struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

// RestoreResponse — audit trail post-restore.
type RestoreResponse struct {
	TenantID   string    `json:"tenant_id"`
	Status     string    `json:"status"` // "active"
	RestoredAt time.Time `json:"restored_at"`
}

// PurgeInput est le body de POST /api/tenants/{id}/purge.
// confirm doit valoir litteralement "PURGE" pour proceder (safeguard
// destructif). reason est obligatoire (audit GDPR).
type PurgeInput struct {
	TenantID string `json:"tenant_id"`
	Reason   string `json:"reason"`
	Confirm  string `json:"confirm"`
}

// PurgeResponse — audit trail post-purge.
type PurgeResponse struct {
	TenantID string    `json:"tenant_id"`
	Status   string    `json:"status"` // "purged"
	PurgedAt time.Time `json:"purged_at"`
}

// TouchResponse — audit trail post-touch (debounced).
type TouchResponse struct {
	TenantID  string    `json:"tenant_id"`
	TouchedAt time.Time `json:"touched_at"`
	Debounced bool      `json:"debounced"` // true si no-op silencieux (touch < 24h)
}

// UsageSummaryResponse agrege l'utilisation effective d'un tenant (sec. 5.8).
type UsageSummaryResponse struct {
	TenantID         string     `json:"tenant_id"`
	MessagesSent30d  int64      `json:"messages_sent_30d"`
	LastActivityAt   *time.Time `json:"last_activity_at,omitempty"`
	ContactsCount    int64      `json:"contacts_count"`
	Plan             string     `json:"plan"`
	Status           PlanStatus `json:"status"`
	GeneratedAt      time.Time  `json:"generated_at"`
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

// LimitsResponse est la reponse de GET /api/veridian/limits — expose au
// caller (console UI, paywall middleware, agent Hub debug) l'integralite
// des limites + dimensions feature pour un tenant. La struct est concue
// pour s'enrichir progressivement avec l'usage actuel (lots 4+) sans
// breaking change sur la surface JSON.
//
// Convention : -1 = illimite. Les booleens feature sont en clair (vs nullable)
// car ils ont toujours une valeur (false par defaut sur Free).
//
// Cf. ticket todo/2026-05-20-pricing-plans-implementation.md livrable 8.
type LimitsResponse struct {
	TenantID    string     `json:"tenant_id"`
	Plan        string     `json:"plan"`
	PlanSource  PlanSource `json:"plan_source"`
	Status      PlanStatus `json:"status"`
	Limits      PlanLimits `json:"limits"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// VeridianService est l'interface des operations Hub-driven.
type VeridianService interface {
	Provision(ctx context.Context, input ProvisionInput) (*ProvisionResponse, error)
	// UpdatePlan modifie le plan d'un tenant. Renvoie un audit trail
	// (previous_plan, plan_source applique, applied_at) CONTRAT-HUB sec. 5.2.
	// Erreurs metier : ErrPlanImmune (lifetime/manual ecrase par stripe).
	UpdatePlan(ctx context.Context, input UpdatePlanInput) (*UpdatePlanResponse, error)
	Suspend(ctx context.Context, input SuspendInput) error
	Resume(ctx context.Context, input ResumeInput) error
	// SoftDelete marque le tenant comme soft-deleted. CONTRAT-HUB sec. 5.7-5.8.
	// reason = "" toleree pour back-compat handler DELETE legacy.
	// Renvoie un audit (purge_eligible_at) — calle a NOW + 30j.
	SoftDelete(ctx context.Context, input SoftDeleteInput) (*SoftDeleteResponse, error)
	// Restore annule un soft-delete. Refuse si le tenant n'est pas soft-deleted
	// (ErrTenantNotSoftDeleted).
	Restore(ctx context.Context, input RestoreInput) (*RestoreResponse, error)
	// Purge supprime DEFINITIVEMENT (hard delete) un tenant. Refuse si
	// purge_eligible_at > NOW (ErrPurgeNotEligible). Exige confirm=="PURGE".
	Purge(ctx context.Context, input PurgeInput) (*PurgeResponse, error)
	// Touch met a jour le heartbeat anti-soft-delete (last_touched_at).
	// Debounced 24h en service : si touche dans les 24h, no-op silencieux.
	Touch(ctx context.Context, tenantID string) (*TouchResponse, error)
	// UsageSummary agrege l'utilisation effective du tenant (messages 30j,
	// contacts, derniere activite). CONTRAT-HUB sec. 5.8.
	UsageSummary(ctx context.Context, tenantID string) (*UsageSummaryResponse, error)
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

	// === Veridian patch === Grant unlimited access (équipe interne + clients
	// fideles + partenaires). Passe le tenant en plan=enterprise + quota=-1 +
	// plan_source=lifetime_partner (par defaut). Immune aux downgrades Stripe.
	// Idempotent. reason obligatoire pour audit GDPR/compta.
	GrantUnlimited(ctx context.Context, input GrantUnlimitedInput) (*GrantUnlimitedResponse, error)

	// === Veridian patch V37 === GetLimits retourne les limites + dimensions
	// feature effectives pour un tenant (lus depuis veridian_plan). Source de
	// verite pour le paywall middleware (lot 4), l'endpoint /api/veridian/limits
	// (lot 7), et le UI console (widgets quota).
	//
	// La priorite est : valeurs DB (qui matchent backfill V37 + overrides
	// custom Hub) → fallback PlanLimits depuis LimitsForPlan(plan) si la
	// row a toutes les dimensions a zero (cas tenant antedeluvien jamais
	// re-upsert apres la migration). Retourne sql.ErrNoRows si tenant absent.
	GetLimits(ctx context.Context, tenantID string) (*LimitsResponse, error)
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
	// EventTenantDeleted : conserve pour back-compat avec les consommateurs
	// Hub existants. Le service emet maintenant EventTenantSoftDeleted en
	// parallele (les deux events sont push pour eviter de casser le Hub
	// pendant la transition).
	EventTenantDeleted VeridianEvent = "tenant.deleted"
	// === Lifecycle (CONTRAT-HUB sec. 5.7-5.8) ===
	EventTenantSoftDeleted VeridianEvent = "tenant.soft_deleted"
	EventTenantRestored    VeridianEvent = "tenant.restored"
	EventTenantPurged      VeridianEvent = "tenant.purged"
	EventTenantTouched     VeridianEvent = "tenant.touched"

	EventTenantPlanChanged VeridianEvent = "tenant.plan_changed"
	EventEmailSent         VeridianEvent = "email.sent"
	EventEmailBounced      VeridianEvent = "email.bounced"
	EventEmailComplaint    VeridianEvent = "email.complaint"
	EventQuotaExceeded     VeridianEvent = "tenant.quota_exceeded"
)

// VeridianEventPayload est le payload signe envoye au Hub.
//
// Champs alias `event` et `idempotency_key` : ajoutés pour s'aligner sur le
// contrat README intégrations Hub (cf. veridian-hub/todo/integrations/README.md
// §"Format webhook standard") sans casser les consommateurs qui se basent
// déjà sur `event_type` et `event_id`. MarshalJSON injecte les 4 champs.
type VeridianEventPayload struct {
	EventID    string                 `json:"event_id"` // UUID, pour idempotence cote Hub
	EventType  VeridianEvent          `json:"event_type"`
	TenantID   string                 `json:"tenant_id"`
	OccurredAt time.Time              `json:"occurred_at"`
	Data       map[string]interface{} `json:"data,omitempty"`
}

// MarshalJSON ajoute les alias `event` (= event_type) et `idempotency_key`
// (= event_id) au payload sortant, pour conformité contrat Hub v1.
// Les anciens champs `event_type` et `event_id` sont conservés afin de ne
// pas casser les consommateurs existants — additif uniquement.
func (p VeridianEventPayload) MarshalJSON() ([]byte, error) {
	type alias VeridianEventPayload
	envelope := struct {
		alias
		Event          VeridianEvent `json:"event"`
		IdempotencyKey string        `json:"idempotency_key"`
	}{
		alias:          alias(p),
		Event:          p.EventType,
		IdempotencyKey: p.EventID,
	}
	return json.Marshal(envelope)
}

// WebhookEmitter envoie des events au Hub.
type WebhookEmitter interface {
	Emit(ctx context.Context, eventType VeridianEvent, tenantID string, data map[string]interface{})
}
