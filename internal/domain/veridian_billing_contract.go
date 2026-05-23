package domain

// === Veridian patch — CONTRAT-BILLING v2.0 alignment (2026-05-23) ===
//
// Constantes et helpers du contrat billing cross-app Veridian v2.
// Source de vérité : veridian-hub/docs/CONTRAT-BILLING.md §3 (payload
// update-plan v2 versionné).
//
// Ce fichier ne modifie aucun type upstream. Il étend le domaine Veridian
// avec les invariants v2 que `veridian_update_plan_handler.go` consomme :
//
//   - Versioning : `contract_version` major doit être supporté.
//   - Enum `plan` fermé : free | pro | business | enterprise.
//   - Enum `plan_source` v2 : stripe | stripe_trial | grant_manual |
//     downgrade_auto (cf §3.3).
//   - Mapping legacy v1 → v2 : les valeurs `manual`, `lifetime_*`, `internal`
//     remontent comme `grant_manual` en sortie d'API (preservées en DB pour
//     traçabilité).
//
// Les anciennes constantes PlanSource* (PlanSourceManual, PlanSourceInternal,
// PlanSourceLifetime*) restent valides côté lecture/écriture pour compat
// V37+ et données existantes. Voir NormalizePlanSourceV2() pour le mapping
// d'exposition côté contrat v2.

import "strings"

// === Versioning du contrat billing v2 ===

// SupportedContractMajor est le major du contrat billing supporté côté
// Notifuse. Le Hub envoie `contract_version: "2.x"`. Un major différent
// (e.g. "3.0") doit être rejeté `400 invalid_payload` (cf CONTRAT-BILLING
// §3.4.1).
const SupportedContractMajor = "2"

// CurrentContractVersion est la version exacte du contrat actuellement
// implémentée. Émise dans les réponses pour aider au debug cross-app.
const CurrentContractVersion = "2.0"

// IsSupportedContractVersion renvoie true si la version envoyée par le Hub
// est compatible avec l'implémentation actuelle.
//
// Règles :
//   - Chaîne vide → true (back-compat legacy v1, Hub pas encore migré).
//   - Major == SupportedContractMajor → true (e.g. "2.0", "2.1", "2.99").
//   - Tout autre major → false (e.g. "3.0", "1.5").
//   - Format invalide (pas de `.`) → false.
func IsSupportedContractVersion(v string) bool {
	if v == "" {
		// Legacy v1 : pas de contract_version envoyé. Toléré tant que le
		// Hub n'a pas migré côté lib/notifuse/client.ts. Cf CONTRAT-BILLING
		// §3.4.1 note migration.
		return true
	}
	idx := strings.Index(v, ".")
	if idx <= 0 {
		return false
	}
	return v[:idx] == SupportedContractMajor
}

// === Enum `plan` canonique cross-app (CONTRAT-BILLING §3.2) ===

// CanonicalPlan represente un plan canonique cross-app. L'enum est FERMÉ :
// tout autre valeur reçue dans `update-plan` doit déclencher un `400
// invalid_plan` côté handler.
type CanonicalPlan string

const (
	CanonicalPlanFree       CanonicalPlan = "free"
	CanonicalPlanPro        CanonicalPlan = "pro"
	CanonicalPlanBusiness   CanonicalPlan = "business"
	CanonicalPlanEnterprise CanonicalPlan = "enterprise"
)

// AllowedCanonicalPlans est la liste explicite des plans canoniques. Exposée
// dans les `details.allowed_plans` d'un `400 invalid_plan` pour aider le
// caller (Hub) à se corriger.
var AllowedCanonicalPlans = []string{
	string(CanonicalPlanFree),
	string(CanonicalPlanPro),
	string(CanonicalPlanBusiness),
	string(CanonicalPlanEnterprise),
}

// IsValidCanonicalPlan renvoie true si `plan` ∈ {free, pro, business,
// enterprise}. Chaîne vide → false (gestion séparée des champs requis).
func IsValidCanonicalPlan(plan string) bool {
	switch CanonicalPlan(plan) {
	case CanonicalPlanFree, CanonicalPlanPro, CanonicalPlanBusiness, CanonicalPlanEnterprise:
		return true
	default:
		return false
	}
}

// === Enum `plan_source` v2 (CONTRAT-BILLING §3.3) ===

// Constantes v2 — additionnelles aux PlanSource* legacy déjà déclarées
// dans veridian.go. Les anciennes valeurs restent supportées en lecture et
// en écriture (back-compat données existantes).
const (
	// PlanSourceStripeTrial : la trial state machine Hub a activé un trial
	// Pro 15j. Aucune sub Stripe payante, aucune facture. Distinct de
	// PlanSourceStripe (CONTRAT-BILLING §7.2 — l'app DOIT distinguer pour
	// l'UI "essai" vs "abonné").
	PlanSourceStripeTrial PlanSource = "stripe_trial"

	// PlanSourceGrantManual : plan offert (factorisation v2 des 3 valeurs
	// legacy manual + lifetime_site_vitrine + lifetime_partner + internal).
	// Immune au downgrade Stripe (§3.4.4). Le Hub conserve la granularité
	// lifetime_partner vs internal côté lui — l'app n'a besoin que de
	// savoir "ce plan est immune Stripe".
	PlanSourceGrantManual PlanSource = "grant_manual"

	// PlanSourceDowngradeAuto : décision explicite du Hub de downgrader
	// un tenant (subscription Stripe expirée/annulée, trial sans
	// conversion). L'app applique alors son mode dégradé paywall
	// (CONTRAT-BILLING §5.3). Distinct de PlanSourceStripe (qui veut dire
	// "subscription payante active") pour traçabilité UX.
	PlanSourceDowngradeAuto PlanSource = "downgrade_auto"
)

// AllowedPlanSourcesV2 est la liste des 4 valeurs canoniques du contrat v2.
// Exposée dans les `details.allowed_plan_sources` d'un `400 invalid_payload`.
var AllowedPlanSourcesV2 = []string{
	string(PlanSourceStripe),
	string(PlanSourceStripeTrial),
	string(PlanSourceGrantManual),
	string(PlanSourceDowngradeAuto),
}

// IsValidPlanSourceV2 renvoie true si la valeur correspond à une des 4
// valeurs canoniques v2, OU à une valeur legacy v1 toujours acceptée en
// entrée (back-compat avec les payloads Hub pas encore migrés).
//
// Chaîne vide → true (default Stripe au repo upsert, comportement legacy).
func IsValidPlanSourceV2(s PlanSource) bool {
	switch s {
	case "",
		// v2 canonique
		PlanSourceStripe, PlanSourceStripeTrial, PlanSourceGrantManual, PlanSourceDowngradeAuto,
		// v1 legacy (mappé vers grant_manual en sortie)
		PlanSourceManual, PlanSourceLifetimeSiteVitrine, PlanSourceLifetimePartner, PlanSourceInternal:
		return true
	default:
		return false
	}
}

// NormalizePlanSourceV2 mappe une valeur legacy v1 vers la valeur canonique
// v2 correspondante. Les 4 valeurs v2 sont retournées telles quelles.
//
// Usage : utilisé pour la réponse API (echo `plan_source` côté
// UpdatePlanResponse) afin d'exposer l'enum v2 même si la DB stocke encore
// une valeur legacy v1. Le stockage reste libre (back-compat).
//
//	manual                  → grant_manual
//	lifetime_site_vitrine   → grant_manual
//	lifetime_partner        → grant_manual
//	internal                → grant_manual
//	stripe                  → stripe
//	stripe_trial            → stripe_trial
//	grant_manual            → grant_manual
//	downgrade_auto          → downgrade_auto
//	""                      → ""
func NormalizePlanSourceV2(s PlanSource) PlanSource {
	switch s {
	case PlanSourceManual, PlanSourceLifetimeSiteVitrine, PlanSourceLifetimePartner, PlanSourceInternal:
		return PlanSourceGrantManual
	default:
		return s
	}
}

// IsImmuneV2 renvoie true si le plan_source est immune aux downgrades
// automatiques (Stripe / downgrade_auto). Étend PlanSource.IsImmune()
// pour les valeurs v2.
//
// L'invariant CONTRAT-BILLING §3.4.4 protège grant_manual contre tout
// `update-plan` venant de `stripe` ou `downgrade_auto`. Un admin Hub a
// toujours le dernier mot via un nouvel `update-plan` plan_source=grant_manual.
func IsImmuneV2(s PlanSource) bool {
	switch s {
	case PlanSourceGrantManual,
		// Valeurs legacy équivalentes — mêmes droits d'immunité.
		PlanSourceManual, PlanSourceLifetimeSiteVitrine, PlanSourceLifetimePartner, PlanSourceInternal:
		return true
	default:
		return false
	}
}

// IsAutoDowngradeSource renvoie true si le plan_source représente une
// décision automatique du Hub (stripe webhook ou downgrade_auto). Utilisé
// par l'invariant §3.4.4 : un `update-plan` venant d'une source auto ne
// peut pas écraser un grant_manual.
//
// stripe_trial n'est PAS auto-downgrade : c'est une activation explicite
// décidée par la state machine (donnerait une UI "essai gratuit"). On la
// traite comme une mutation neutre, qui DOIT respecter l'immunité au
// même titre que stripe (un tenant lifetime ne se met pas en trial Pro
// sur un signal d'engagement métier — ça serait incohérent).
func IsAutoDowngradeSource(s PlanSource) bool {
	switch s {
	case PlanSourceStripe, PlanSourceStripeTrial, PlanSourceDowngradeAuto:
		return true
	default:
		return false
	}
}
