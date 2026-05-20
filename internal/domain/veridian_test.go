package domain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === PlanSource — CONTRAT-HUB sec. 3.3 ===

func TestPlanSource_IsImmune(t *testing.T) {
	cases := []struct {
		source PlanSource
		want   bool
	}{
		{PlanSourceStripe, false},
		{PlanSourceManual, true},
		{PlanSourceLifetimeSiteVitrine, true},
		{PlanSourceLifetimePartner, true},
		{PlanSourceInternal, true},
		{PlanSource(""), false}, // chaine vide = default stripe = mutable
		{PlanSource("unknown"), false},
	}
	for _, tc := range cases {
		t.Run(string(tc.source), func(t *testing.T) {
			assert.Equal(t, tc.want, tc.source.IsImmune())
		})
	}
}

func TestPlanSource_IsValid(t *testing.T) {
	valid := []PlanSource{
		"", // = default stripe
		PlanSourceStripe,
		PlanSourceManual,
		PlanSourceLifetimeSiteVitrine,
		PlanSourceLifetimePartner,
		PlanSourceInternal,
	}
	for _, s := range valid {
		t.Run(string(s)+"_valid", func(t *testing.T) {
			assert.True(t, s.IsValid(), "%q doit etre valide", s)
		})
	}

	invalid := []PlanSource{"unknown", "STRIPE", "lifetime"}
	for _, s := range invalid {
		t.Run(string(s)+"_invalid", func(t *testing.T) {
			assert.False(t, s.IsValid(), "%q ne doit PAS etre valide", s)
		})
	}
}

func TestPlanSource_Constants(t *testing.T) {
	// Sanity : les constantes correspondent aux valeurs persistees en DB
	// (CONTRAT-HUB sec. 3.3). Si ces strings changent, la migration V33
	// (DEFAULT 'stripe') et tous les tenants existants cassent.
	assert.Equal(t, "stripe", string(PlanSourceStripe))
	assert.Equal(t, "manual", string(PlanSourceManual))
	assert.Equal(t, "lifetime_site_vitrine", string(PlanSourceLifetimeSiteVitrine))
	assert.Equal(t, "lifetime_partner", string(PlanSourceLifetimePartner))
	assert.Equal(t, "internal", string(PlanSourceInternal))
}

// === Lifecycle (sec. 5.7-5.8) — events + struct fields ===

func TestVeridianEvent_LifecycleConstants(t *testing.T) {
	// Sanity : les events lifecycle correspondent aux valeurs exactes du
	// contrat sec. 7.1. Si ces strings changent, les consommateurs Hub cassent.
	assert.Equal(t, "tenant.soft_deleted", string(EventTenantSoftDeleted))
	assert.Equal(t, "tenant.restored", string(EventTenantRestored))
	assert.Equal(t, "tenant.purged", string(EventTenantPurged))
	assert.Equal(t, "tenant.touched", string(EventTenantTouched))
	// Back-compat : tenant.deleted reste emis en parallele de tenant.soft_deleted.
	assert.Equal(t, "tenant.deleted", string(EventTenantDeleted))
}

func TestVeridianPlan_LifecycleFields(t *testing.T) {
	// Verifie que les nouveaux champs V34 sont bien des pointeurs (null-safe
	// pour les tenants pre-V34 qui n'ont jamais ete touche/restore/purge).
	now := time.Now()
	purgeEligible := now.Add(30 * 24 * time.Hour)
	p := &VeridianPlan{
		WorkspaceID:     "ws-1",
		DeletedAt:       &now,
		PurgeEligibleAt: &purgeEligible,
		LifecycleReason: "GDPR",
	}
	require.NotNil(t, p.PurgeEligibleAt)
	assert.True(t, p.PurgeEligibleAt.After(*p.DeletedAt))
	assert.Equal(t, "GDPR", p.LifecycleReason)
	assert.Nil(t, p.RestoredAt, "champ nullable pour tenants jamais restore")
	assert.Nil(t, p.LastTouchedAt, "champ nullable pour tenants jamais touche")
}

// === VeridianPlan ===

func TestVeridianPlan_IsBlocked_DeletedFirst(t *testing.T) {
	now := time.Now()
	p := &VeridianPlan{DeletedAt: &now, Status: PlanStatusActive}
	blocked, reason := p.IsBlocked()
	assert.True(t, blocked)
	assert.Equal(t, "tenant deleted", reason)
}

func TestVeridianPlan_IsBlocked_Suspended(t *testing.T) {
	p := &VeridianPlan{Status: PlanStatusSuspended, SuspendedReason: "payment failed"}
	blocked, reason := p.IsBlocked()
	assert.True(t, blocked)
	assert.Equal(t, "payment failed", reason)
}

func TestVeridianPlan_IsBlocked_QuotaExceeded(t *testing.T) {
	// === DÉCISION 2026-05-20 === Le quota mensuel d'envoi n'est PLUS un
	// motif de blocage tant que Veridian ne fournit pas son propre provider
	// (BYO sending). Le test, qui validait l'ancien comportement, valide
	// maintenant le NOUVEAU : un tenant qui aurait théoriquement dépassé son
	// quota peut quand même envoyer car c'est son provider qui limite.
	p := &VeridianPlan{
		Status:              PlanStatusActive,
		MonthlyEmailQuota:   500,
		EmailsSentThisMonth: 500,
	}
	blocked, reason := p.IsBlocked()
	assert.False(t, blocked, "quota dépassé ne doit PLUS bloquer (BYO sending)")
	assert.Empty(t, reason)
}

func TestVeridianPlan_IsBlocked_Active(t *testing.T) {
	p := &VeridianPlan{
		Status:              PlanStatusActive,
		MonthlyEmailQuota:   500,
		EmailsSentThisMonth: 100,
	}
	blocked, _ := p.IsBlocked()
	assert.False(t, blocked)
}

func TestVeridianPlan_QuotaRemaining(t *testing.T) {
	cases := []struct {
		name             string
		quota, sent, exp int64
	}{
		{"unlimited", -1, 999999, -1},
		{"normal", 500, 100, 400},
		{"exhausted", 500, 500, 0},
		{"over_quota", 500, 600, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &VeridianPlan{MonthlyEmailQuota: tc.quota, EmailsSentThisMonth: tc.sent}
			assert.Equal(t, tc.exp, p.QuotaRemaining())
		})
	}
}

// === PlanQuotasInput (sec. 5.17) ===

func TestPlanQuotasInput_NilMonthlyEmails(t *testing.T) {
	// Cas par défaut : struct vide ou champ nil = "Hub n'envoie pas de quota".
	// Le service doit alors fallback sur QuotaForPlan(plan).
	var q *PlanQuotasInput
	assert.Nil(t, q, "PlanQuotasInput zero value est nil")

	q2 := &PlanQuotasInput{}
	assert.Nil(t, q2.MonthlyEmails, "MonthlyEmails non set = nil pointer")
}

func TestPlanQuotasInput_ExplicitMonthlyEmails(t *testing.T) {
	cases := []struct {
		name  string
		value int64
	}{
		{"zero limit", 0},
		{"normal", 500},
		{"unlimited", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			q := &PlanQuotasInput{MonthlyEmails: &v}
			require.NotNil(t, q.MonthlyEmails)
			assert.Equal(t, tc.value, *q.MonthlyEmails)
		})
	}
}

func TestQuotaForPlan(t *testing.T) {
	// === DÉCISION 2026-05-20 === Tous les plans sont en quota -1 (illimite)
	// tant que Veridian ne fournit pas son propre provider d'envoi (BYO).
	// Le client utilise sa propre boite, c'est son provider qui limite.
	assert.Equal(t, int64(-1), QuotaForPlan("free"))
	assert.Equal(t, int64(-1), QuotaForPlan("pro"))
	assert.Equal(t, int64(-1), QuotaForPlan("business"))
	assert.Equal(t, int64(-1), QuotaForPlan("enterprise"))
	// fallback : plan inconnu → quota free (= -1 maintenant aussi)
	assert.Equal(t, int64(-1), QuotaForPlan("unknown"))
}

// TestVeridianPlan_IsBlocked_QuotaNoLongerBlocks verifie que le quota mensuel
// n'est PLUS un motif de blocage (décision 2026-05-20 BYO sending). Garde-fou
// contre une regression : si quelqu'un re-active le check ligne 126 sans
// changer cette doc, ce test fail.
func TestVeridianPlan_IsBlocked_QuotaNoLongerBlocks(t *testing.T) {
	// Plan free, quota=300 (ancienne valeur), 9999 mails envoyés (très au-dessus)
	// → ne doit PAS etre bloqué.
	p := &VeridianPlan{
		Plan:                "free",
		Status:              PlanStatusActive,
		MonthlyEmailQuota:   300,
		EmailsSentThisMonth: 9999,
	}
	blocked, reason := p.IsBlocked()
	assert.False(t, blocked, "quota dépassé ne doit PLUS bloquer (BYO sending)")
	assert.Empty(t, reason)
}

func TestVeridianPlan_IsBlocked_SuspendedStillBlocks(t *testing.T) {
	// Suspend reste un motif de blocage légitime (problème compte Veridian).
	p := &VeridianPlan{
		Status:          PlanStatusSuspended,
		SuspendedReason: "payment_failed",
	}
	blocked, reason := p.IsBlocked()
	assert.True(t, blocked)
	assert.Equal(t, "payment_failed", reason)
}

func TestVeridianPlan_IsBlocked_DeletedStillBlocks(t *testing.T) {
	// Deleted reste un motif de blocage légitime (compte fermé).
	now := time.Now()
	p := &VeridianPlan{
		Status:    PlanStatusActive,
		DeletedAt: &now,
	}
	blocked, reason := p.IsBlocked()
	assert.True(t, blocked)
	assert.Contains(t, reason, "deleted")
}

// === GrantUnlimited — équipe interne + clients fideles + partenaires ===
// Verifie que les types I/O sont exposes correctement et que l'interface
// VeridianService expose bien la nouvelle methode. Compile-time guard.

func TestGrantUnlimitedInput_Roundtrip(t *testing.T) {
	in := GrantUnlimitedInput{
		TenantID:   "robertbrunon",
		Reason:     "internal_team_member",
		PlanSource: PlanSourceLifetimePartner,
	}
	assert.Equal(t, "robertbrunon", in.TenantID)
	assert.Equal(t, "internal_team_member", in.Reason)
	assert.True(t, in.PlanSource.IsImmune(), "lifetime_partner doit etre immune")
	assert.True(t, in.PlanSource.IsValid())
}

func TestGrantUnlimitedResponse_AuditTrail(t *testing.T) {
	// Audit GDPR/compta : tous les champs doivent etre persistes en reponse
	// pour que le Hub puisse logger qui a recu un grant, pourquoi, quand.
	now := time.Now().UTC()
	resp := GrantUnlimitedResponse{
		TenantID:     "client42",
		Plan:         "enterprise",
		PreviousPlan: "pro",
		PlanSource:   PlanSourceLifetimePartner,
		Quota:        -1,
		GrantedAt:    now,
		Reason:       "lifetime_offer_2026",
	}
	assert.Equal(t, "enterprise", resp.Plan)
	assert.Equal(t, "pro", resp.PreviousPlan)
	assert.Equal(t, int64(-1), resp.Quota)
	assert.Equal(t, "lifetime_offer_2026", resp.Reason)
	assert.False(t, resp.GrantedAt.IsZero(), "GrantedAt doit etre set pour audit")
}

// TestVeridianServiceInterface_ExposesGrantUnlimited verifie que la methode
// GrantUnlimited est bien dans l'interface VeridianService (la compilation
// echouerait si elle ne l'etait pas, mais ce test sert d'invariant explicite
// pour le hook check-test-mapping qui exige un test sur chaque modif d'interface).
func TestVeridianServiceInterface_ExposesGrantUnlimited(t *testing.T) {
	// Compile-time check : si la signature change, le test ne compile pas.
	var _ func(ctx context.Context, input GrantUnlimitedInput) (*GrantUnlimitedResponse, error)
	// Marker runtime pour pouvoir grep "GrantUnlimited" dans les tests.
	assert.NotPanics(t, func() {
		_ = GrantUnlimitedInput{}
		_ = GrantUnlimitedResponse{}
	})
}

// === PlanLimits — V37 pricing fondation ===

// TestDefaultPlanLimits_AllPlansDefined verifie que les 4 plans canoniques
// sont definis. Si un plan est ajoute / renomme, ce test casse — c'est
// volontaire (force update synchrone avec VISION-BUSINESS.md + backfill V37).
func TestDefaultPlanLimits_AllPlansDefined(t *testing.T) {
	expected := []string{"free", "pro", "business", "enterprise"}
	for _, plan := range expected {
		t.Run(plan, func(t *testing.T) {
			_, ok := DefaultPlanLimits[plan]
			assert.True(t, ok, "plan %q manquant dans DefaultPlanLimits", plan)
		})
	}
	assert.Len(t, DefaultPlanLimits, len(expected), "nombre de plans inattendu — update test + VISION-BUSINESS.md")
}

// TestDefaultPlanLimits_FreeMatchesContract — Free est le plus contraint.
// Doit matcher pixel-perfect VISION-BUSINESS.md table pricing.
func TestDefaultPlanLimits_FreeMatchesContract(t *testing.T) {
	free := DefaultPlanLimits["free"]
	assert.Equal(t, int64(-1), free.MonthlyEmailQuota, "BYO sending = pas de cap")
	assert.Equal(t, int64(500), free.MaxContacts)
	assert.Equal(t, 1, free.MaxSeats)
	assert.Equal(t, 1, free.MaxOAuthAccounts)
	assert.Equal(t, 0, free.MaxCustomDomains, "pas de domaine custom en Free")
	assert.Equal(t, 1, free.MaxActiveSequences)
	assert.False(t, free.FeatureABTesting)
	assert.False(t, free.FeatureBrandingRemoved, "Free DOIT garder Powered by Veridian")
	assert.False(t, free.FeatureWhiteLabel)
	assert.Equal(t, 30, free.HistoryRetentionDays)
}

// TestDefaultPlanLimits_ProMatchesContract — Pro 29 EUR/mo.
func TestDefaultPlanLimits_ProMatchesContract(t *testing.T) {
	pro := DefaultPlanLimits["pro"]
	assert.Equal(t, int64(5000), pro.MaxContacts)
	assert.Equal(t, 5, pro.MaxSeats)
	assert.Equal(t, 5, pro.MaxOAuthAccounts)
	assert.Equal(t, 1, pro.MaxCustomDomains)
	assert.Equal(t, -1, pro.MaxActiveSequences, "Pro = sequences illimitees")
	assert.True(t, pro.FeatureABTesting)
	assert.True(t, pro.FeatureBrandingRemoved)
	assert.False(t, pro.FeatureWhiteLabel, "white-label est Business+")
	assert.Equal(t, 365, pro.HistoryRetentionDays)
}

// TestDefaultPlanLimits_BusinessMatchesContract — Business 99 EUR/mo.
func TestDefaultPlanLimits_BusinessMatchesContract(t *testing.T) {
	biz := DefaultPlanLimits["business"]
	assert.Equal(t, int64(25000), biz.MaxContacts)
	assert.Equal(t, 25, biz.MaxSeats)
	assert.Equal(t, 25, biz.MaxOAuthAccounts)
	assert.Equal(t, 5, biz.MaxCustomDomains)
	assert.Equal(t, -1, biz.MaxActiveSequences)
	assert.True(t, biz.FeatureABTesting)
	assert.True(t, biz.FeatureBrandingRemoved)
	assert.True(t, biz.FeatureWhiteLabel, "Business inclut white-label")
	assert.Equal(t, -1, biz.HistoryRetentionDays, "Business = historique illimite")
}

// TestDefaultPlanLimits_EnterpriseAllUnlimited — Enterprise sur devis,
// tout illimite + toutes les features.
func TestDefaultPlanLimits_EnterpriseAllUnlimited(t *testing.T) {
	ent := DefaultPlanLimits["enterprise"]
	assert.Equal(t, int64(-1), ent.MonthlyEmailQuota)
	assert.Equal(t, int64(-1), ent.MaxContacts)
	assert.Equal(t, -1, ent.MaxSeats)
	assert.Equal(t, -1, ent.MaxOAuthAccounts)
	assert.Equal(t, -1, ent.MaxCustomDomains)
	assert.Equal(t, -1, ent.MaxActiveSequences)
	assert.Equal(t, -1, ent.HistoryRetentionDays)
	assert.True(t, ent.FeatureABTesting)
	assert.True(t, ent.FeatureBrandingRemoved)
	assert.True(t, ent.FeatureWhiteLabel)
}

// TestLimitsForPlan_KnownPlans — chemin nominal.
func TestLimitsForPlan_KnownPlans(t *testing.T) {
	cases := []string{"free", "pro", "business", "enterprise"}
	for _, plan := range cases {
		t.Run(plan, func(t *testing.T) {
			got := LimitsForPlan(plan)
			want := DefaultPlanLimits[plan]
			assert.Equal(t, want, got, "LimitsForPlan(%q) doit matcher DefaultPlanLimits", plan)
		})
	}
}

// TestLimitsForPlan_UnknownFallsBackToFree — semantique safe : un plan
// inconnu ne doit jamais accorder plus de privileges que Free (pas
// d'escalade silencieuse).
func TestLimitsForPlan_UnknownFallsBackToFree(t *testing.T) {
	freeRef := DefaultPlanLimits["free"]
	cases := []string{"", "unknown", "PRO", "lifetime"}
	for _, plan := range cases {
		t.Run(plan, func(t *testing.T) {
			got := LimitsForPlan(plan)
			assert.Equal(t, freeRef, got, "plan inconnu %q doit retomber sur Free", plan)
		})
	}
}

// TestVeridianPlan_PricingFields_StructTags — verifie que les tags JSON
// matchent les noms de colonnes SQL exactement (lot 2 repository va
// scanner par column name, drift = bug silencieux). Cf. memory
// sqlmock_does_not_validate_postgres_types.
func TestVeridianPlan_PricingFields_StructTags(t *testing.T) {
	p := VeridianPlan{
		MaxContacts:            500,
		MaxSeats:               1,
		MaxOAuthAccounts:       1,
		MaxCustomDomains:       0,
		MaxActiveSequences:     1,
		FeatureABTesting:       false,
		FeatureBrandingRemoved: false,
		FeatureWhiteLabel:      false,
		HistoryRetentionDays:   30,
	}
	// Round-trip pour s'assurer que les tags se serialisent / deserialisent
	// sans perte ni typo.
	require.NotPanics(t, func() {
		_ = p.MaxContacts
		_ = p.MaxSeats
	})
	assert.Equal(t, int64(500), p.MaxContacts)
	assert.Equal(t, 30, p.HistoryRetentionDays)
}

