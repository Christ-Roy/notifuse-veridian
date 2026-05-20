package domain

// === Veridian patch ===
// Types pour l'endpoint POST /api/veridian/admin/grant-unlimited.
//
// Usage : Robert (ou un automation Hub) appelle ce endpoint pour offrir un
// tenant illimite a un membre interne de l'equipe Veridian ou a un client
// fidele / partenaire. Le tenant est passe en plan=enterprise + quota=-1 +
// plan_source=lifetime_partner (par defaut, configurable).
//
// Effets de bord :
//   - veridian_plan.plan = "enterprise"
//   - veridian_plan.monthly_email_quota = -1 (illimite)
//   - veridian_plan.plan_source = "lifetime_partner" (immune au downgrade
//     Stripe via le garde-fou IsImmune() dans veridian.go)
//   - veridian_plan.status = "active" (resume si suspended)
//   - Cache paywall invalide immediatement pour effet instant.
//   - Emit event tenant.plan_changed avec previous_plan/new_plan.
//
// Idempotent : appel sur un tenant deja enterprise/lifetime → no-op
// silencieux (mais re-emet l'event pour audit trail). reason est obligatoire
// pour l'audit GDPR/compta — qui a recu un cadeau, pourquoi.

import "time"

// GrantUnlimitedInput est le body de POST /api/veridian/admin/grant-unlimited.
//
// TenantID : workspace_id cible (doit exister, sinon 404).
// Reason : pourquoi on offre l'acces illimite (audit). Obligatoire.
//   Exemples : "internal_team_member", "lifetime_partner_robert_xyz",
//   "compensation_outage_2026_05_20".
// PlanSource : optionnel, par defaut "lifetime_partner". Doit etre une
//   PlanSource immune (lifetime_partner, lifetime_site_vitrine, manual,
//   internal). "" → defaut. "stripe" → refuse (un grant manuel ne peut pas
//   etre attribue a Stripe).
type GrantUnlimitedInput struct {
	TenantID   string     `json:"tenant_id"`
	Reason     string     `json:"reason"`
	PlanSource PlanSource `json:"plan_source,omitempty"`
}

// GrantUnlimitedResponse — audit trail post-grant.
type GrantUnlimitedResponse struct {
	TenantID     string     `json:"tenant_id"`
	Plan         string     `json:"plan"`          // "enterprise"
	PreviousPlan string     `json:"previous_plan"` // plan d'origine (free, pro, ...)
	PlanSource   PlanSource `json:"plan_source"`   // applied source
	Quota        int64      `json:"quota"`         // -1
	GrantedAt    time.Time  `json:"granted_at"`
	Reason       string     `json:"reason"`
}
