package domain

import "testing"

// Tests colocalisés pour veridian_billing_contract.go (CONTRAT-BILLING v2).
// Chaque func exportée du fichier source a au moins un TestXxx ici (règle
// 1-pour-1 du pre-push hook).

func TestIsSupportedContractVersion(t *testing.T) {
	cases := []struct {
		name     string
		version  string
		expected bool
	}{
		{"empty string (legacy v1 back-compat)", "", true},
		{"v2.0 supported", "2.0", true},
		{"v2.1 supported (minor bump)", "2.1", true},
		{"v2.99 supported (minor)", "2.99", true},
		{"v3.0 unsupported (major bump)", "3.0", false},
		{"v1.5 unsupported (older major)", "1.5", false},
		{"malformed no dot", "2", false},
		{"malformed empty major", ".0", false},
		{"malformed garbage", "abc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSupportedContractVersion(tc.version); got != tc.expected {
				t.Errorf("IsSupportedContractVersion(%q) = %v, want %v", tc.version, got, tc.expected)
			}
		})
	}
}

func TestIsValidCanonicalPlan(t *testing.T) {
	cases := []struct {
		plan     string
		expected bool
	}{
		{"free", true},
		{"pro", true},
		{"business", true},
		{"enterprise", true},
		{"", false},
		{"Free", false},      // case-sensitive
		{"freemium", false},  // nom local Prospection, pas canonique
		{"premium", false},
		{"foo", false},
	}
	for _, tc := range cases {
		t.Run(tc.plan, func(t *testing.T) {
			if got := IsValidCanonicalPlan(tc.plan); got != tc.expected {
				t.Errorf("IsValidCanonicalPlan(%q) = %v, want %v", tc.plan, got, tc.expected)
			}
		})
	}
}

func TestIsValidPlanSourceV2(t *testing.T) {
	cases := []struct {
		name     string
		src      PlanSource
		expected bool
	}{
		// v2 canonique
		{"empty (defaults to stripe)", "", true},
		{"stripe", PlanSourceStripe, true},
		{"stripe_trial (v2 new)", PlanSourceStripeTrial, true},
		{"grant_manual (v2 new)", PlanSourceGrantManual, true},
		{"downgrade_auto (v2 new)", PlanSourceDowngradeAuto, true},
		// legacy v1 (back-compat in)
		{"manual (legacy)", PlanSourceManual, true},
		{"lifetime_partner (legacy)", PlanSourceLifetimePartner, true},
		{"lifetime_site_vitrine (legacy)", PlanSourceLifetimeSiteVitrine, true},
		{"internal (legacy)", PlanSourceInternal, true},
		// invalid
		{"garbage", PlanSource("garbage"), false},
		{"stripe_active fake", PlanSource("stripe_active"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidPlanSourceV2(tc.src); got != tc.expected {
				t.Errorf("IsValidPlanSourceV2(%q) = %v, want %v", tc.src, got, tc.expected)
			}
		})
	}
}

func TestNormalizePlanSourceV2(t *testing.T) {
	cases := []struct {
		name string
		in   PlanSource
		want PlanSource
	}{
		{"manual legacy → grant_manual", PlanSourceManual, PlanSourceGrantManual},
		{"lifetime_site_vitrine → grant_manual", PlanSourceLifetimeSiteVitrine, PlanSourceGrantManual},
		{"lifetime_partner → grant_manual", PlanSourceLifetimePartner, PlanSourceGrantManual},
		{"internal → grant_manual", PlanSourceInternal, PlanSourceGrantManual},
		{"stripe → stripe (unchanged)", PlanSourceStripe, PlanSourceStripe},
		{"stripe_trial → stripe_trial (unchanged)", PlanSourceStripeTrial, PlanSourceStripeTrial},
		{"grant_manual → grant_manual (unchanged)", PlanSourceGrantManual, PlanSourceGrantManual},
		{"downgrade_auto → downgrade_auto (unchanged)", PlanSourceDowngradeAuto, PlanSourceDowngradeAuto},
		{"empty → empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePlanSourceV2(tc.in); got != tc.want {
				t.Errorf("NormalizePlanSourceV2(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsImmuneV2(t *testing.T) {
	// v2 valeur immune + valeurs legacy équivalentes
	immunes := []PlanSource{
		PlanSourceGrantManual,
		PlanSourceManual,
		PlanSourceLifetimeSiteVitrine,
		PlanSourceLifetimePartner,
		PlanSourceInternal,
	}
	for _, src := range immunes {
		if !IsImmuneV2(src) {
			t.Errorf("IsImmuneV2(%q) = false, want true", src)
		}
	}
	// non-immune (auto downgrade ou activation)
	nonImmunes := []PlanSource{
		PlanSourceStripe,
		PlanSourceStripeTrial,
		PlanSourceDowngradeAuto,
		"",
		"garbage",
	}
	for _, src := range nonImmunes {
		if IsImmuneV2(src) {
			t.Errorf("IsImmuneV2(%q) = true, want false", src)
		}
	}
}

func TestIsAutoDowngradeSource(t *testing.T) {
	// sources auto (peuvent déclencher downgrade ou activation auto)
	auto := []PlanSource{
		PlanSourceStripe,
		PlanSourceStripeTrial,
		PlanSourceDowngradeAuto,
	}
	for _, src := range auto {
		if !IsAutoDowngradeSource(src) {
			t.Errorf("IsAutoDowngradeSource(%q) = false, want true", src)
		}
	}
	// non-auto (admin / legacy lifetime / vide)
	nonAuto := []PlanSource{
		PlanSourceGrantManual,
		PlanSourceManual,
		PlanSourceLifetimePartner,
		PlanSourceLifetimeSiteVitrine,
		PlanSourceInternal,
		"",
		"garbage",
	}
	for _, src := range nonAuto {
		if IsAutoDowngradeSource(src) {
			t.Errorf("IsAutoDowngradeSource(%q) = true, want false", src)
		}
	}
}

// === Invariants croisés (CONTRAT-BILLING §3.4.4) ===
// L'invariant "plan offert immune au downgrade Stripe" doit être codé comme :
//   immune := IsImmuneV2(existing.PlanSource)
//   auto   := IsAutoDowngradeSource(input.PlanSource)
//   if immune && auto → 409 plan_locked
//
// Ce test sanity-check que la combinaison est cohérente sur les paires
// critiques (cas réels que le Hub peut envoyer).
func TestImmunityInvariant_PaywallContract(t *testing.T) {
	type pair struct {
		existing      PlanSource
		incoming      PlanSource
		shouldReject  bool
		desc          string
	}
	cases := []pair{
		// Stripe sur lifetime → bloqué (cas critique §3.4.4)
		{PlanSourceLifetimePartner, PlanSourceStripe, true, "stripe sur lifetime_partner"},
		{PlanSourceGrantManual, PlanSourceStripe, true, "stripe sur grant_manual"},
		// stripe_trial sur lifetime → bloqué aussi (un tenant offert ne se met pas en trial)
		{PlanSourceLifetimePartner, PlanSourceStripeTrial, true, "stripe_trial sur lifetime_partner"},
		// downgrade_auto sur lifetime → bloqué (le tenant offert reste offert)
		{PlanSourceGrantManual, PlanSourceDowngradeAuto, true, "downgrade_auto sur grant_manual"},
		// grant_manual écrasant lifetime → autorisé (admin a le dernier mot)
		{PlanSourceLifetimePartner, PlanSourceGrantManual, false, "admin grant écrase lifetime"},
		{PlanSourceManual, PlanSourceManual, false, "manual→manual (admin re-set)"},
		// Stripe normal (existing stripe) → autorisé
		{PlanSourceStripe, PlanSourceStripe, false, "stripe renewal"},
		{PlanSourceStripe, PlanSourceStripeTrial, false, "trial activation depuis free/stripe"},
		{PlanSourceStripe, PlanSourceDowngradeAuto, false, "downgrade sub Stripe expirée"},
		// Stripe sur free/empty → autorisé (provisioning normal)
		{"", PlanSourceStripe, false, "empty → stripe"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			immune := IsImmuneV2(c.existing)
			auto := IsAutoDowngradeSource(c.incoming)
			rejected := immune && auto
			if rejected != c.shouldReject {
				t.Errorf("existing=%q incoming=%q → rejected=%v, want %v (immune=%v, auto=%v)",
					c.existing, c.incoming, rejected, c.shouldReject, immune, auto)
			}
		})
	}
}
