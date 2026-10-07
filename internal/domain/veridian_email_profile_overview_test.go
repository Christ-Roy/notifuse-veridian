package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailProviderVeridianProfileType(t *testing.T) {
	cases := []struct {
		name     string
		provider *EmailProvider
		want     string
	}{
		{"nil", nil, ""},
		{"smtp générique", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.relai.example"}}, VeridianProfileTypeSMTP},
		{"gmail mot de passe d'application", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.gmail.com"}}, VeridianProfileTypeGmailAppPassword},
		{"gmail oauth", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.gmail.com", AuthType: "oauth2", OAuth2Provider: "google"}}, VeridianProfileTypeGmailOAuth},
		{"oauth google sur un autre hote", &EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.example.test", AuthType: "oauth2", OAuth2Provider: "google"}}, VeridianProfileTypeGmailOAuth},
		{"fournisseur API", &EmailProvider{Kind: EmailProviderKindSES}, "ses"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.provider.VeridianProfileType())
		})
	}
}

func TestVeridianBuildPlanObserved(t *testing.T) {
	rows := []VeridianPlanObservationRow{
		{ProfileID: "p1", SenderEmail: "Hello@Envoi.example", ProviderClass: "google", Accepted: 4},
		{ProfileID: "p1", SenderEmail: "hello@envoi.example", ProviderClass: "microsoft", Accepted: 2},
		{ProfileID: "p2", SenderEmail: "autre@envoi.example", ProviderClass: "google", Accepted: 3},
		{ProfileID: "", SenderEmail: "", ProviderClass: "google", Accepted: 9},
	}
	counters := []VeridianPlanCounterRow{
		{Kind: VeridianDailyQuotaKindProfile, Scope: "p1", Used: 7},
		{Kind: VeridianDailyQuotaKindProfile, Scope: "p2", Used: 99},
		{Kind: VeridianDailyQuotaKindWarmup, Scope: "envoi.example", Used: 11},
		{Kind: VeridianDailyQuotaKindProviderClass, Scope: "envoi.example", ProviderClass: "google", Used: 8},
	}
	obs := VeridianBuildPlanObserved("p1", rows, counters)
	assert.Equal(t, 6, obs.ProfileAccepted, "seulement les envois du profil")
	assert.Equal(t, 7, obs.ProfileReserved, "le compteur du profil, pas celui d'un autre")
	assert.Equal(t, 9, obs.DomainSent["envoi.example"], "le domaine compte tous les profils qui l'utilisent")
	assert.Equal(t, 6, obs.SenderSent["hello@envoi.example"], "adresses comptées en minuscules")
	assert.Equal(t, 7, obs.DomainClassSent["envoi.example"]["google"])
	assert.Equal(t, 11, obs.DomainReserved["envoi.example"])
	assert.Equal(t, 8, obs.DomainClassReserved["envoi.example"]["google"])
}

func TestVeridianEmailProfilesOverviewJSONContract(t *testing.T) {
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
