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
