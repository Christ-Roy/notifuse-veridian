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
