package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianAggregateEmailProfileUsage(t *testing.T) {
	gmail := func(id string, cap int) Integration {
		verified := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		return Integration{ID: id, Type: IntegrationTypeEmail, EmailProvider: EmailProvider{
			Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp.gmail.com"}, VeridianProfileDailyCap: cap, VeridianTransportVerifiedAt: &verified,
		}}
	}
	w := &Workspace{Settings: WorkspaceSettings{VeridianMarketingEmailProviderIDs: []string{"p1", "p2"}}, Integrations: []Integration{gmail("p1", 30), gmail("p2", 40)}}
	result := VeridianAggregateEmailProfileUsage(w, []VeridianEmailProfileUsageRow{
		{ProfileID: "p1", ReservedUsed: 5},
		{ProfileID: "p1", ProviderClass: ProviderClassGoogle, AcceptedUsed: 3},
		{ProfileID: "p1", ProviderClass: ProviderClassMicrosoft, AcceptedUsed: 1},
		{ProfileID: "p2", ReservedUsed: 6},
		{ProfileID: "p2", ProviderClass: "unknown", AcceptedUsed: 5},
	}, time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
	require.Len(t, result.Profiles, 2)
	assert.Equal(t, 11, result.TotalUsed)
	assert.Equal(t, 9, result.TotalAccepted)
	assert.Equal(t, 5, result.Profiles[0].Used, "quota usage comes from reservations, not accepted history")
	assert.Equal(t, 4, result.Profiles[0].AcceptedUsed)
	assert.Equal(t, 25, result.Profiles[0].Remaining)
	assert.Equal(t, 3, result.Profiles[0].AcceptedByProviderClass[ProviderClassGoogle])
	assert.Equal(t, 5, result.Profiles[1].AcceptedByProviderClass[VeridianProviderClassUnclassified])
	assert.Zero(t, result.Profiles[1].AcceptedByProviderClass[ProviderClassCorporate], "unknown destinations must not contaminate corporate analytics")
}
