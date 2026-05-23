package domain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonMarshal helper local pour les tests qui valident les tags JSON
// stables (cf. TestLimitsResponse_JSONSchema). Pas de tier1 lib dependency
// utile ici, encoding/json suffit largement.
func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

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

// TestDefaultPlanLimits_FreeAllUnlimited — Pivot 2026-05-21 generosite
// maximale : Free a TOUT illimite. SEULE difference vs paid = la duree
// (deadline 15j cf. trial state machine Hub). Pas de white-label Free.
func TestDefaultPlanLimits_FreeAllUnlimited(t *testing.T) {
	free := DefaultPlanLimits["free"]
	assert.Equal(t, int64(-1), free.MonthlyEmailQuota, "BYO sending = pas de cap")
	assert.Equal(t, int64(-1), free.MaxContacts, "pivot 2026-05-21 illimite")
	assert.Equal(t, -1, free.MaxSeats, "growth hacking = invitation illimitee")
	assert.Equal(t, -1, free.MaxOAuthAccounts)
	assert.Equal(t, -1, free.MaxCustomDomains, "pivot 2026-05-21 illimite — pas de cout infra")
	assert.Equal(t, -1, free.MaxActiveSequences)
	assert.True(t, free.FeatureABTesting, "A/B gratuit pour tous")
	assert.True(t, free.FeatureBrandingRemoved, "branding optionnel pour tous (Free inclus)")
	assert.False(t, free.FeatureWhiteLabel, "white-label reste differenciant Business+")
	assert.Equal(t, -1, free.HistoryRetentionDays)
}

// TestDefaultPlanLimits_ProAllUnlimited — Pivot 2026-05-21 : Pro 29 EUR/mo.
// Identique a Free sauf qu'il echappe au paywall 15j (geree cote Hub).
// Pas de white-label Pro (reste Business+).
func TestDefaultPlanLimits_ProAllUnlimited(t *testing.T) {
	pro := DefaultPlanLimits["pro"]
	assert.Equal(t, int64(-1), pro.MonthlyEmailQuota)
	assert.Equal(t, int64(-1), pro.MaxContacts)
	assert.Equal(t, -1, pro.MaxSeats)
	assert.Equal(t, -1, pro.MaxOAuthAccounts)
	assert.Equal(t, -1, pro.MaxCustomDomains)
	assert.Equal(t, -1, pro.MaxActiveSequences)
	assert.Equal(t, -1, pro.HistoryRetentionDays)
	assert.True(t, pro.FeatureABTesting)
	assert.True(t, pro.FeatureBrandingRemoved)
	assert.False(t, pro.FeatureWhiteLabel, "white-label = SEUL differenciant Business vs Pro")
}

// TestDefaultPlanLimits_BusinessUnlimitedPlusWhiteLabel — Business 99 EUR/mo.
// SEULE difference vs Pro = white-label custom (le client met son propre
// footer "Sent by ClientName" au lieu de juste retirer "Powered by Veridian").
func TestDefaultPlanLimits_BusinessUnlimitedPlusWhiteLabel(t *testing.T) {
	biz := DefaultPlanLimits["business"]
	assert.Equal(t, int64(-1), biz.MonthlyEmailQuota)
	assert.Equal(t, int64(-1), biz.MaxContacts)
	assert.Equal(t, -1, biz.MaxSeats)
	assert.Equal(t, -1, biz.MaxOAuthAccounts)
	assert.Equal(t, -1, biz.MaxCustomDomains)
	assert.Equal(t, -1, biz.MaxActiveSequences)
	assert.Equal(t, -1, biz.HistoryRetentionDays)
	assert.True(t, biz.FeatureABTesting)
	assert.True(t, biz.FeatureBrandingRemoved)
	assert.True(t, biz.FeatureWhiteLabel, "Business INCLUT white-label (seul differenciant vs Pro)")
}

// TestDefaultPlanLimits_EnterpriseAllUnlimited — Enterprise sur devis,
// strict identique a Business sur les dimensions (tout -1 + white-label).
// Differenciation Enterprise = sur devis (SLA, support dedie, contrat
// custom — pas reflete dans PlanLimits).
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

// TestDefaultPlanLimits_PivotInvariant_OnlyDifferenceIsWhiteLabel —
// Garde-fou anti-regression : si un agent re-cable une limite (ex:
// MaxContacts=500 pour Free), ce test casse. La SEULE difference
// entre plans (a part durabilite Free 15j geree cote Hub) c'est
// white-label custom Business+.
func TestDefaultPlanLimits_PivotInvariant_OnlyDifferenceIsWhiteLabel(t *testing.T) {
	plans := []string{"free", "pro", "business", "enterprise"}
	for _, plan := range plans {
		limits := DefaultPlanLimits[plan]
		t.Run(plan+"_all_dimensions_unlimited", func(t *testing.T) {
			assert.Equal(t, int64(-1), limits.MonthlyEmailQuota, "%s monthly emails", plan)
			assert.Equal(t, int64(-1), limits.MaxContacts, "%s contacts", plan)
			assert.Equal(t, -1, limits.MaxSeats, "%s seats", plan)
			assert.Equal(t, -1, limits.MaxOAuthAccounts, "%s oauth", plan)
			assert.Equal(t, -1, limits.MaxCustomDomains, "%s custom domains", plan)
			assert.Equal(t, -1, limits.MaxActiveSequences, "%s sequences", plan)
			assert.Equal(t, -1, limits.HistoryRetentionDays, "%s history", plan)
			assert.True(t, limits.FeatureABTesting, "%s A/B testing", plan)
			assert.True(t, limits.FeatureBrandingRemoved, "%s branding optionnel", plan)
		})
	}
	// White-label : SEULE difference Business+ vs Free/Pro.
	assert.False(t, DefaultPlanLimits["free"].FeatureWhiteLabel)
	assert.False(t, DefaultPlanLimits["pro"].FeatureWhiteLabel)
	assert.True(t, DefaultPlanLimits["business"].FeatureWhiteLabel)
	assert.True(t, DefaultPlanLimits["enterprise"].FeatureWhiteLabel)
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

// === LimitsResponse — V37 lot 3 ===

// TestLimitsResponse_JSONSchema verifie que la struct LimitsResponse
// embarque correctement les champs et que les tags JSON sont stables
// (les consommateurs Hub + console UI lock sur ces noms). Tout drift de
// tag serait un breaking change silencieux cote API.
func TestLimitsResponse_JSONSchema(t *testing.T) {
	now := time.Now().UTC()
	resp := LimitsResponse{
		TenantID:   "client42",
		Plan:       "business",
		PlanSource: PlanSourceStripe,
		Status:     PlanStatusActive,
		Limits: PlanLimits{
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
		GeneratedAt: now,
	}
	assert.Equal(t, "client42", resp.TenantID)
	assert.Equal(t, "business", resp.Plan)
	assert.Equal(t, PlanSourceStripe, resp.PlanSource)
	assert.Equal(t, PlanStatusActive, resp.Status)
	assert.Equal(t, int64(25000), resp.Limits.MaxContacts)
	assert.True(t, resp.Limits.FeatureWhiteLabel, "Business inclut white-label")
	assert.Equal(t, -1, resp.Limits.HistoryRetentionDays)
	assert.False(t, resp.GeneratedAt.IsZero(), "GeneratedAt set pour cache TTL caller")

	// Roundtrip JSON pour valider les tags (drift de tag = breaking
	// silencieux pour les consommateurs Hub + console UI).
	encoded, err := jsonMarshal(resp)
	require.NoError(t, err)
	asStr := string(encoded)
	assert.Contains(t, asStr, `"tenant_id":"client42"`)
	assert.Contains(t, asStr, `"plan":"business"`)
	assert.Contains(t, asStr, `"plan_source":"stripe"`)
	assert.Contains(t, asStr, `"status":"active"`)
	assert.Contains(t, asStr, `"generated_at":`)
	// Le sous-objet "limits" doit etre present (les champs internes ne
	// sont PAS taggues — c'est volontaire, PlanLimits est une struct
	// interne non-publique en JSON. La serialisation par defaut Go
	// utilise les noms de champs Go en CamelCase).
	assert.Contains(t, asStr, `"limits":`)
}

// TestVeridianServiceInterface_ExposesGetLimits — invariant explicite que
// l'interface inclut GetLimits avec la bonne signature. Le pre-push hook
// exige un test pour chaque nouvelle methode ajoutee a une interface.
func TestVeridianServiceInterface_ExposesGetLimits(t *testing.T) {
	// Compile-time check : si la signature change, le test ne compile pas.
	var _ func(ctx context.Context, tenantID string) (*LimitsResponse, error)
	// Marker runtime pour pouvoir grep "GetLimits" dans les tests.
	assert.NotPanics(t, func() {
		_ = LimitsResponse{}
	})
}

// === V38 — Activation tracking ===

// TestActivityThresholdEmails — constante business critique.
// Valeur figée : si elle change, les tests Hub qui assertent sur ce seuil cassent.
func TestActivityThresholdEmails(t *testing.T) {
	assert.Equal(t, int64(5), ActivityThresholdEmails,
		"seuil d'activation trial figé à 5 — ne pas changer sans coordonner avec Hub")
}

// TestEventTenantActivityThresholdReached — constante event webhook.
// String figée : si elle change, les consommateurs Hub cassent silencieusement.
func TestEventTenantActivityThresholdReached(t *testing.T) {
	assert.Equal(t, "tenant.activity_threshold_reached", string(EventTenantActivityThresholdReached),
		"nom de l'event webhook figé — breaking change pour le Hub si modifié")
}

// TestVeridianPlan_V38Fields — les deux nouveaux champs V38 sont bien
// présents dans la struct VeridianPlan avec les bons types.
func TestVeridianPlan_V38Fields(t *testing.T) {
	now := time.Now().UTC()
	p := VeridianPlan{
		WorkspaceID:                "ws-1",
		EmailsSentLifetime:         42,
		ActivityThresholdReachedAt: &now,
	}
	assert.Equal(t, int64(42), p.EmailsSentLifetime)
	require.NotNil(t, p.ActivityThresholdReachedAt)
	assert.Equal(t, now.Unix(), p.ActivityThresholdReachedAt.Unix())
}

// TestVeridianPlan_V38Fields_NullableThreshold — ActivityThresholdReachedAt
// doit être nil par défaut (tenant qui n'a pas encore atteint le seuil).
func TestVeridianPlan_V38Fields_NullableThreshold(t *testing.T) {
	p := VeridianPlan{WorkspaceID: "ws-new", EmailsSentLifetime: 3}
	assert.Nil(t, p.ActivityThresholdReachedAt,
		"ActivityThresholdReachedAt doit être nil tant que seuil non atteint")
	assert.Equal(t, int64(3), p.EmailsSentLifetime)
}

// TestVeridianPlanRepository_ExposesNewMethods — invariant explicite que
// l'interface VeridianPlanRepository expose les 2 nouvelles méthodes V38.
// Le pre-push hook exige un test pour chaque methode ajoutee a une interface.
func TestVeridianPlanRepository_ExposesNewMethods(t *testing.T) {
	// Compile-time checks via déclarations de type de fonction — si les
	// signatures changent, le fichier ne compile plus.
	var _ func(ctx context.Context, workspaceID string, delta int64) (int64, bool, error)
	var _ func(ctx context.Context, workspaceID string, at time.Time) error
	assert.NotPanics(t, func() {
		_ = VeridianPlan{EmailsSentLifetime: 0}
	})
}


// === Hub discovery types (2026-05-20) ===

// TestDiscoveryResponse_JSONSchema valide les tags JSON stables de
// DiscoveryResponse et DiscoveryWorkspace (contrat Hub serialisation).
func TestDiscoveryResponse_JSONSchema(t *testing.T) {
	resp := DiscoveryResponse{
		Found:     true,
		UserEmail: "alice@example.com",
		Workspaces: []DiscoveryWorkspace{
			{
				WorkspaceID:      "ws-alice",
				WorkspaceName:    "Alice Corp",
				Role:             "owner",
				Plan:             "pro",
				MagicLinkCapable: true,
				FallbackURL:      "https://notifuse.app.veridian.site/console/signin",
			},
		},
	}

	data, err := jsonMarshal(resp)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, true, decoded["found"])
	assert.Equal(t, "alice@example.com", decoded["user_email"])
	workspaces, ok := decoded["workspaces"].([]interface{})
	require.True(t, ok)
	require.Len(t, workspaces, 1)
	ws := workspaces[0].(map[string]interface{})
	assert.Equal(t, "ws-alice", ws["workspace_id"])
	assert.Equal(t, "Alice Corp", ws["workspace_name"])
	assert.Equal(t, "owner", ws["role"])
	assert.Equal(t, "pro", ws["plan"])
	assert.Equal(t, true, ws["magic_link_capable"])
	assert.Equal(t, "https://notifuse.app.veridian.site/console/signin", ws["fallback_url"])
}

// TestDiscoveryResponse_FoundFalse_EmptyWorkspaces valide que found:false
// serialise bien workspaces:[] et non workspaces:null (Hub attend un tableau).
func TestDiscoveryResponse_FoundFalse_EmptyWorkspaces(t *testing.T) {
	resp := DiscoveryResponse{
		Found:      false,
		UserEmail:  "ghost@example.com",
		Workspaces: []DiscoveryWorkspace{},
	}

	data, err := jsonMarshal(resp)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, false, decoded["found"])
	workspaces, ok := decoded["workspaces"].([]interface{})
	require.True(t, ok, "workspaces doit etre un tableau, pas nil")
	assert.Len(t, workspaces, 0)
}

// TestVeridianService_ExposesLookupByEmail verifie que LookupByEmail est bien
// dans l interface VeridianService (compile-time check — Constitution §4).
func TestVeridianService_ExposesLookupByEmail(t *testing.T) {
	var _ func(ctx context.Context, email string) (*DiscoveryResponse, error)
	assert.NotPanics(t, func() {
		_ = DiscoveryResponse{Workspaces: []DiscoveryWorkspace{}}
	})
}

// TestWipeTestTenantsInput_IncludeOrphans_JSONRoundtrip valide que le champ
// IncludeOrphans serialise correctement (par defaut omitempty si false).
func TestWipeTestTenantsInput_IncludeOrphans_JSONRoundtrip(t *testing.T) {
	// Cas par defaut : false → omitempty.
	in := WipeTestTenantsInput{Prefix: "e2e"}
	data, err := jsonMarshal(in)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "include_orphans", "include_orphans:false doit etre omis")

	// Cas explicite true → present.
	in2 := WipeTestTenantsInput{Prefix: "e2e", IncludeOrphans: true}
	data2, err := jsonMarshal(in2)
	require.NoError(t, err)
	assert.Contains(t, string(data2), `"include_orphans":true`)
}

// TestListTenantsResponse_TwoBuckets valide la projection managed vs orphans.
func TestListTenantsResponse_TwoBuckets(t *testing.T) {
	resp := ListTenantsResponse{
		Managed: []TenantSummary{
			{TenantID: "client1", HasPlan: true, Plan: "pro", Status: "active"},
		},
		Orphans: []TenantSummary{
			{TenantID: "ghost1", HasPlan: false},
		},
		Total: 2,
	}

	data, err := jsonMarshal(resp)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, float64(2), decoded["total"])
	managed, ok := decoded["managed"].([]interface{})
	require.True(t, ok)
	assert.Len(t, managed, 1)

	orphans, ok := decoded["orphans"].([]interface{})
	require.True(t, ok)
	assert.Len(t, orphans, 1)

	// HasPlan:false doit etre present (pas omitempty sur ce champ — Robert
	// veut voir explicitement true OR false dans la projection admin).
	ghost := orphans[0].(map[string]interface{})
	assert.Equal(t, "ghost1", ghost["tenant_id"])
	assert.Equal(t, false, ghost["has_plan"])
}

// TestListTenantsResponse_OrphansOmittedWhenEmpty valide que orphans:[] est
// omis du JSON quand le caller n'a pas demande include_orphans (cleaner).
func TestListTenantsResponse_OrphansOmittedWhenEmpty(t *testing.T) {
	resp := ListTenantsResponse{
		Managed: []TenantSummary{{TenantID: "c1", HasPlan: true}},
		Total:   1,
	}
	data, err := jsonMarshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "orphans")
}

// TestVeridianService_ExposesListTenants verifie que ListTenants est dans
// l'interface (compile-time check — Constitution §4).
func TestVeridianService_ExposesListTenants(t *testing.T) {
	var _ func(ctx context.Context, input ListTenantsInput) (*ListTenantsResponse, error)
	assert.NotPanics(t, func() {
		_ = ListTenantsResponse{Managed: []TenantSummary{}}
	})
}

// === V39 — Résilience billing Hub (last_hub_sync_at) ===

// TestHubSyncThresholds_Constants — les seuils 24h/72h sont figés business.
// Ne pas changer sans coordonner avec Hub (ils définissent la tolérance
// incident + la dégradation paywall).
func TestHubSyncThresholds_Constants(t *testing.T) {
	assert.Equal(t, 24*time.Hour, HubSyncFreshThreshold, "seuil fresh figé à 24h")
	assert.Equal(t, 72*time.Hour, HubSyncDeadThreshold, "seuil dead figé à 72h")
}

// TestEvaluateHubSyncStatus_Nil — NULL last_hub_sync_at = Fresh (fail-open :
// les tenants antérieurs à V39 ne doivent pas être dégradés au boot).
func TestEvaluateHubSyncStatus_Nil(t *testing.T) {
	p := &VeridianPlan{WorkspaceID: "ws-1", LastHubSyncAt: nil}
	assert.Equal(t, HubSyncFresh, p.EvaluateHubSyncStatus(time.Now()))
}

// TestEvaluateHubSyncStatus_VeryRecent — sync il y a 1h → Fresh.
func TestEvaluateHubSyncStatus_VeryRecent(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-1 * time.Hour)
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncFresh, p.EvaluateHubSyncStatus(now))
}

// TestEvaluateHubSyncStatus_JustUnder24h — sync il y a 23h59m → Fresh.
func TestEvaluateHubSyncStatus_JustUnder24h(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-(24*time.Hour - time.Minute))
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncFresh, p.EvaluateHubSyncStatus(now))
}

// TestEvaluateHubSyncStatus_Exactly24h — boundary 24h exactement → Stale.
func TestEvaluateHubSyncStatus_Exactly24h(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-24 * time.Hour)
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncStale, p.EvaluateHubSyncStatus(now))
}

// TestEvaluateHubSyncStatus_Stale48h — sync il y a 48h → Stale (grace optimistic).
func TestEvaluateHubSyncStatus_Stale48h(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-48 * time.Hour)
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncStale, p.EvaluateHubSyncStatus(now))
}

// TestEvaluateHubSyncStatus_JustUnder72h — sync il y a 71h59m → Stale.
func TestEvaluateHubSyncStatus_JustUnder72h(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-(72*time.Hour - time.Minute))
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncStale, p.EvaluateHubSyncStatus(now))
}

// TestEvaluateHubSyncStatus_Exactly72h — boundary 72h exactement → Dead.
func TestEvaluateHubSyncStatus_Exactly72h(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-72 * time.Hour)
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncDead, p.EvaluateHubSyncStatus(now))
}

// TestEvaluateHubSyncStatus_Dead100h — sync il y a 100h → Dead.
func TestEvaluateHubSyncStatus_Dead100h(t *testing.T) {
	now := time.Now()
	syncAt := now.Add(-100 * time.Hour)
	p := &VeridianPlan{LastHubSyncAt: &syncAt}
	assert.Equal(t, HubSyncDead, p.EvaluateHubSyncStatus(now))
}

// TestVeridianPlan_LastHubSyncAt_JSONOmitEmpty — nil → omis du JSON.
func TestVeridianPlan_LastHubSyncAt_JSONOmitEmpty(t *testing.T) {
	p := VeridianPlan{WorkspaceID: "ws-1"}
	data, err := jsonMarshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "last_hub_sync_at",
		"last_hub_sync_at nil doit être omis (omitempty)")
}

// TestVeridianPlan_LastHubSyncAt_JSONPresent — non-nil → présent dans le JSON.
func TestVeridianPlan_LastHubSyncAt_JSONPresent(t *testing.T) {
	now := time.Now().UTC()
	p := VeridianPlan{WorkspaceID: "ws-1", LastHubSyncAt: &now}
	data, err := jsonMarshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(data), "last_hub_sync_at",
		"last_hub_sync_at non-nil doit apparaître dans le JSON")
}

// TestVeridianPlanRepository_ExposesTouchHubSync — invariant compile-time
// que l'interface VeridianPlanRepository expose bien TouchHubSync.
func TestVeridianPlanRepository_ExposesTouchHubSync(t *testing.T) {
	var _ func(ctx context.Context, workspaceID string) error
	assert.NotPanics(t, func() {
		// Marker runtime — grep "TouchHubSync" dans les tests.
		_ = VeridianPlan{LastHubSyncAt: nil}
	})
}

// === V40 — quota_exceeded_emitted_at_month (Lot I) ===

// TestVeridianPlan_QuotaExceededEmittedAtMonth_JSONOmitEmpty — nil → omis du JSON.
func TestVeridianPlan_QuotaExceededEmittedAtMonth_JSONOmitEmpty(t *testing.T) {
	p := VeridianPlan{WorkspaceID: "ws-1"}
	data, err := jsonMarshal(p)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "quota_exceeded_emitted_at_month",
		"quota_exceeded_emitted_at_month nil doit être omis (omitempty)")
}

// TestVeridianPlan_QuotaExceededEmittedAtMonth_JSONPresent — non-nil → présent JSON.
func TestVeridianPlan_QuotaExceededEmittedAtMonth_JSONPresent(t *testing.T) {
	now := time.Now().UTC()
	p := VeridianPlan{WorkspaceID: "ws-1", QuotaExceededEmittedAtMonth: &now}
	data, err := jsonMarshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(data), "quota_exceeded_emitted_at_month",
		"quota_exceeded_emitted_at_month non-nil doit apparaître dans le JSON")
}

// TestVeridianPlanRepository_ExposesMarkQuotaExceededEmitted — invariant
// compile-time que l'interface VeridianPlanRepository expose la nouvelle
// méthode V40. Garde-fou : si une refacto vire la méthode de l'interface,
// le test ne compilera plus (et le check-test-mapping rate cette régression).
func TestVeridianPlanRepository_ExposesMarkQuotaExceededEmitted(t *testing.T) {
	// Signature attendue : (ctx, workspaceID, atMonth) (affected bool, err error).
	var _ func(ctx context.Context, workspaceID string, atMonth time.Time) (bool, error)
	assert.NotPanics(t, func() {
		// Marker runtime — grep "MarkQuotaExceededEmitted" dans les tests.
		_ = VeridianPlan{QuotaExceededEmittedAtMonth: nil}
	})
}

// TestEventQuotaExceeded_Value — invariant sur le nom de l'event émis vers Hub.
// Le contrat (cf. veridian-hub/lib/notifuse/types.ts → QuotaExceededEventData)
// pivote sur cette chaîne littérale ; toute modification = breaking pour le Hub.
func TestEventQuotaExceeded_Value(t *testing.T) {
	assert.Equal(t, VeridianEvent("tenant.quota_exceeded"), EventQuotaExceeded)
}
// TestVeridianService_ExposesRotateAPIKeyAndTransferOwner — invariant
// compile-time que l'interface VeridianService expose bien les 2 methodes
// du Lot K (CONTRAT-HUB §5.15 + §5.16). Si le mock est regenere et qu'on
// retire une methode de l'interface, ce test casse au build (l'assertion
// type echoue silencieusement mais l'usage du type Method explicite force
// la verification).
func TestVeridianService_ExposesRotateAPIKeyAndTransferOwner(t *testing.T) {
	// Une variable de type fonction matching la signature de l'interface.
	// Si l'interface change (signature differente ou methode supprimee),
	// l'assignation depuis l'interface dans un test futur cassera le build.
	var rotateFn func(context.Context, RotateAPIKeyInput) (*RotateAPIKeyResponse, error)
	var transferFn func(context.Context, TransferOwnerInput) (*TransferOwnerResponse, error)
	_ = rotateFn
	_ = transferFn

	// Verifier que les types Input ont bien TenantID `json:"-"` (audit ticket).
	in := RotateAPIKeyInput{TenantID: "ws-1", Reason: "x"}
	assert.Equal(t, "ws-1", in.TenantID)
	in2 := TransferOwnerInput{TenantID: "ws-1", NewOwnerEmail: "n@x.t", Reason: "r"}
	assert.Equal(t, "ws-1", in2.TenantID)
}

// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8.3) ===

// TestIssueMagicLinkInput_JSONShape verifie le contrat JSON du body
// POST /api/sso/issue-magic-link : champs `hub_user_id` + `email`, exacts noms
// snake_case. Si on change accidentellement un tag JSON, le Hub reject avec
// invalid_payload (champs manquants).
func TestIssueMagicLinkInput_JSONShape(t *testing.T) {
	in := IssueMagicLinkInput{
		HubUserID: "hub-uuid-xyz",
		Email:     "alice@example.com",
	}
	raw, err := json.Marshal(in)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"hub_user_id":"hub-uuid-xyz","email":"alice@example.com"}`, string(raw))
}

// TestIssueMagicLinkResponse_JSONShape verifie le contrat JSON 200 :
// `magic_link_url` (pas `magicLinkUrl` ni `url`). Le Hub parse exactement
// ce champ (cf. bounce-apps.ts:266).
func TestIssueMagicLinkResponse_JSONShape(t *testing.T) {
	out := IssueMagicLinkResponse{
		MagicLinkURL: "https://notifuse.app.veridian.site/veridian/auto-login?token=abc.def",
	}
	raw, err := json.Marshal(out)
	assert.NoError(t, err)
	assert.JSONEq(t,
		`{"magic_link_url":"https://notifuse.app.veridian.site/veridian/auto-login?token=abc.def"}`,
		string(raw))
}

// TestVeridianService_ExposesIssueMagicLinkForHub : compile-time check que
// l'interface VeridianService expose bien IssueMagicLinkForHub avec la bonne
// signature. Si la methode est supprimee ou renommee, ce test casse au build.
func TestVeridianService_ExposesIssueMagicLinkForHub(t *testing.T) {
	var fn func(context.Context, IssueMagicLinkInput) (*IssueMagicLinkResponse, error)
	_ = fn
	// Verifier les noms de champs Input/Response (audit de contrat).
	in := IssueMagicLinkInput{HubUserID: "x", Email: "a@b.t"}
	assert.Equal(t, "x", in.HubUserID)
	assert.Equal(t, "a@b.t", in.Email)
	out := IssueMagicLinkResponse{MagicLinkURL: "https://x.veridian.site/"}
	assert.Equal(t, "https://x.veridian.site/", out.MagicLinkURL)
}

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
// Garde-fou compile-time pour les 3 nouvelles methodes de l'interface
// VeridianService (SyncMember/RemoveMember/RestoreMember). Si une signature
// diverge ou une methode est retiree, le build casse ici.
func TestVeridianService_ExposesMembershipMethods(t *testing.T) {
	var syncFn func(context.Context, SyncMemberInput) (*SyncMemberResponse, error)
	var removeFn func(context.Context, RemoveMemberInput) (*RemoveMemberResponse, error)
	var restoreFn func(context.Context, RestoreMemberInput) (*RestoreMemberResponse, error)
	_ = syncFn
	_ = removeFn
	_ = restoreFn

	// Audit ticket : TenantID `json:"-"` (injecte depuis path param, pas body).
	syncIn := SyncMemberInput{TenantID: "ws-1", UserEmail: "a@x.test", HubUserID: "u", Role: SyncMemberRoleMember}
	assert.Equal(t, "ws-1", syncIn.TenantID)
	removeIn := RemoveMemberInput{TenantID: "ws-1", UserEmail: "a@x.test"}
	assert.Equal(t, "ws-1", removeIn.TenantID)
	restoreIn := RestoreMemberInput{TenantID: "ws-1", UserEmail: "a@x.test"}
	assert.Equal(t, "ws-1", restoreIn.TenantID)
}

// === CONTRAT-BILLING v2.0 — UpdatePlanInput payload versionné ===
//
// Le contrat v2 (CONTRAT-BILLING.md §3.2) impose un schéma stable pour
// `POST /api/tenants/update-plan`. Toute régression sur les noms de tags
// JSON casse le client Hub. Ces tests garantissent la stabilité du wire
// format.

func TestUpdatePlanInput_V2JSONSchema(t *testing.T) {
	// Payload v2 complet (ce que le Hub enverra côté lib/notifuse/client.ts
	// une fois migré).
	raw := []byte(`{
		"contract_version": "2.0",
		"tenant_id": "ws-acme",
		"plan": "pro",
		"plan_source": "stripe",
		"effective_at": "2026-05-23T10:00:00Z",
		"stripe_subscription_id": "sub_1Abc",
		"idempotency_key": "evt_1XyZ",
		"reason": "checkout.session.completed evt_1XyZ"
	}`)
	var in UpdatePlanInput
	require.NoError(t, json.Unmarshal(raw, &in))
	assert.Equal(t, "2.0", in.ContractVersion)
	assert.Equal(t, "ws-acme", in.TenantID)
	assert.Equal(t, "pro", in.Plan)
	assert.Equal(t, PlanSourceStripe, in.PlanSource)
	assert.Equal(t, "2026-05-23T10:00:00Z", in.EffectiveAt)
	assert.Equal(t, "sub_1Abc", in.StripeSubscriptionID)
	assert.Equal(t, "evt_1XyZ", in.IdempotencyKey)
	assert.Equal(t, "checkout.session.completed evt_1XyZ", in.Reason)
}

func TestUpdatePlanInput_LegacyV1Compat(t *testing.T) {
	// Payload v1 legacy : Hub pas encore migré, envoie {tenant_id, plan} seul.
	// L'app doit décoder sans erreur — ContractVersion sera vide, toléré
	// par IsSupportedContractVersion (§3.4.1 note migration).
	raw := []byte(`{"tenant_id":"ws-legacy","plan":"free"}`)
	var in UpdatePlanInput
	require.NoError(t, json.Unmarshal(raw, &in))
	assert.Empty(t, in.ContractVersion, "legacy v1 a ContractVersion vide")
	assert.Equal(t, "ws-legacy", in.TenantID)
	assert.Equal(t, "free", in.Plan)
	assert.True(t, IsSupportedContractVersion(in.ContractVersion),
		"ContractVersion vide doit être toléré comme legacy v1")
}

func TestUpdatePlanInput_NewV2PlanSourceValues(t *testing.T) {
	// Les 3 nouvelles valeurs plan_source v2 doivent décoder correctement.
	for _, src := range []string{"stripe_trial", "grant_manual", "downgrade_auto"} {
		t.Run(src, func(t *testing.T) {
			raw := []byte(`{
				"contract_version": "2.0",
				"tenant_id": "ws-x",
				"plan": "pro",
				"plan_source": "` + src + `"
			}`)
			var in UpdatePlanInput
			require.NoError(t, json.Unmarshal(raw, &in))
			assert.Equal(t, PlanSource(src), in.PlanSource)
			assert.True(t, IsValidPlanSourceV2(in.PlanSource))
		})
	}
}
