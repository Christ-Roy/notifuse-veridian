package domain

import (
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
	p := &VeridianPlan{
		Status:              PlanStatusActive,
		MonthlyEmailQuota:   500,
		EmailsSentThisMonth: 500,
	}
	blocked, reason := p.IsBlocked()
	assert.True(t, blocked)
	assert.Equal(t, "monthly email quota exceeded", reason)
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
	assert.Equal(t, int64(500), QuotaForPlan("free"))
	assert.Equal(t, int64(10000), QuotaForPlan("pro"))
	assert.Equal(t, int64(50000), QuotaForPlan("business"))
	assert.Equal(t, int64(-1), QuotaForPlan("enterprise"))
	// fallback : plan inconnu → quota free
	assert.Equal(t, int64(500), QuotaForPlan("unknown"))
}

