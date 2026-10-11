package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianEffectivePlanJSONContract(t *testing.T) {
	cap := 30
	overview := VeridianEmailProfilesOverview{
		Date: "2026-10-08",
		Profiles: []VeridianEmailProfileOverview{{
			IntegrationID: "p1", Type: VeridianProfileTypeSMTP, Usage: VeridianProfileUsageCommercial,
			Plan: VeridianEffectivePlan{DailyCapToday: &cap, LimitingGate: VeridianPlanGateProfileCap},
		}},
	}
	raw, err := json.Marshal(overview)
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	profile := generic["profiles"].([]any)[0].(map[string]any)
	for _, key := range []string{"integration_id", "type", "usage", "in_rotation", "paused", "verified", "verified_at", "senders", "return_inbox", "plan"} {
		assert.Contains(t, profile, key)
	}
	plan := profile["plan"].(map[string]any)
	for _, key := range []string{"daily_cap_today", "limiting_gate", "remaining_today", "gates", "window", "excluded_classes", "classes", "sendable_now", "blocked_by", "warmup"} {
		assert.Contains(t, plan, key)
	}
	assert.EqualValues(t, 30, plan["daily_cap_today"])
	assert.NotContains(t, string(raw), "password")
}

func TestVeridianEffectivePlanUnlimitedSerializesAsNull(t *testing.T) {
	raw, err := json.Marshal(VeridianEffectivePlan{LimitingGate: VeridianPlanGateNone})
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	assert.Nil(t, generic["daily_cap_today"], "aucun plafond = null, pas 0 (0 voudrait dire bloque)")
	assert.Nil(t, generic["remaining_today"])
	assert.Equal(t, "none", generic["limiting_gate"])
}

func TestVeridianPlanGateAndBlockNamesAreStable(t *testing.T) {
	// Contrat avec la console et le CLI : ces noms sont affiches et testes ailleurs.
	assert.Equal(t, []string{"warmup", "profile_cap", "per_sender", "class_cap", "none"},
		[]string{VeridianPlanGateWarmup, VeridianPlanGateProfileCap, VeridianPlanGatePerSender, VeridianPlanGateClassCap, VeridianPlanGateNone})
	assert.Equal(t, "paused", VeridianPlanBlockPaused)
	assert.Equal(t, "window_closed", VeridianPlanBlockWindowClosed)
	assert.Equal(t, "reputation_stopped", VeridianPlanBlockReputationStopped)
}

// Lot 3 : l'ecran « Profils d'envoi » affiche « Passerelles anti-spam : debit ÷2,
// 9,9 % de rejets ». Le taux et le volume du couple font partie du contrat.
func TestVeridianPlanClassSlowdownRateJSONContract(t *testing.T) {
	raw, err := json.Marshal(VeridianPlanClass{
		Class: "security_gateway", Factor: 2, Reason: "hard_bounce_rate", SlowdownRate: 0.099, Sent7d: 101,
	})
	require.NoError(t, err)
	var generic map[string]any
	require.NoError(t, json.Unmarshal(raw, &generic))
	assert.InDelta(t, 0.099, generic["slowdown_rate"], 0.0001)
	assert.EqualValues(t, 101, generic["sent_7d"])
	assert.EqualValues(t, 2, generic["slowdown_factor"])
	assert.Equal(t, "hard_bounce_rate", generic["slowdown_reason"])

	// Une classe saine n'embarque pas de taux inutile.
	raw, err = json.Marshal(VeridianPlanClass{Class: "google", Factor: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "slowdown_rate")
	assert.NotContains(t, string(raw), "sent_7d")
}

// Lot 4 (08/10/2026) : le plan dit de quel jour de compte il parle.
func TestVeridianEffectivePlanExposesTheDayOfTheProfile(t *testing.T) {
	raw, err := json.Marshal(VeridianEffectivePlan{Date: "2026-10-09", DayTimezone: "Europe/Paris"})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"date":"2026-10-09"`)
	assert.Contains(t, string(raw), `"day_timezone":"Europe/Paris"`)
	assert.Contains(t, string(raw), `"day_start"`)
	assert.Contains(t, string(raw), `"day_end"`)
}

// Lot 6 : le contrat JSON de la capacité du jour. Les clés sont lues par la console et le
// CLI agent ; une valeur absente (illimité) sort en null, jamais en 0.
func TestVeridianPlanCapacityJSONContract(t *testing.T) {
	configured := 120
	plan := VeridianEffectivePlan{Capacity: VeridianPlanCapacity{
		Applicable: true, Sent: 10, Configured: &configured, Limiting: VeridianCapacityLimitClassRates,
		StockKnown: true, ProfileBoundGate: VeridianPlanGateNone,
	}, Classes: []VeridianPlanClass{{Class: "ovh", CapacityLimiter: VeridianCapacityLimiterRate}}}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	capacity := got["capacity"].(map[string]any)
	assert.Equal(t, float64(120), capacity["configured"])
	assert.Nil(t, capacity["useful"], "capacité utile inconnue = null, pas 0")
	assert.Equal(t, "class_rates", capacity["limiting"])
	for _, key := range []string{"applicable", "sent", "remaining_configured", "stock_known", "profile_bound", "profile_bound_gate",
		"sum_remaining", "flat_cap", "window_minutes_left", "window_minutes_full", "full_day_configured", "full_day_limiting"} {
		assert.Contains(t, capacity, key)
	}
	class := got["classes"].([]any)[0].(map[string]any)
	for _, key := range []string{"capacity_remaining", "capacity_configured", "capacity_useful", "stock", "capacity_limiter"} {
		assert.Contains(t, class, key)
	}
	assert.Equal(t, "rate", class["capacity_limiter"])
}
