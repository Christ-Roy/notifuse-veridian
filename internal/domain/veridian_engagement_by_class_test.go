package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianAggregateEngagementByClass(t *testing.T) {
	t.Run("empty rows -> all canonical classes at zero, total zero", func(t *testing.T) {
		res := VeridianAggregateEngagementByClass(nil)
		require.NotNil(t, res)
		// All canonical classes present with zero counters.
		for _, class := range VeridianAllProviderClasses() {
			agg, ok := res.ByClass[class]
			require.True(t, ok, "class %s must be present", class)
			assert.Equal(t, VeridianClassEngagement{}, agg)
		}
		assert.Equal(t, VeridianClassEngagement{}, res.Total)
	})

	t.Run("classifies by suffix and aggregates per class", func(t *testing.T) {
		rows := []VeridianDomainEngagementRow{
			{Domain: "gmail.com", Sent: 100, Delivered: 95, Bounced: 3, Opened: 40, Clicked: 10},
			{Domain: "googlemail.com", Sent: 10, Delivered: 9, Bounced: 1, Opened: 4, Clicked: 1},
			{Domain: "outlook.com", Sent: 50, Delivered: 48, Bounced: 2, Opened: 20, Clicked: 5},
			{Domain: "some-unknown-corp.example", Sent: 7, Delivered: 6, Bounced: 1, Opened: 2, Clicked: 0},
		}
		res := VeridianAggregateEngagementByClass(rows)

		// gmail + googlemail -> google
		assert.Equal(t, 110, res.ByClass[ProviderClassGoogle].Sent)
		assert.Equal(t, 104, res.ByClass[ProviderClassGoogle].Delivered)
		assert.Equal(t, 4, res.ByClass[ProviderClassGoogle].Bounced)
		assert.Equal(t, 44, res.ByClass[ProviderClassGoogle].Opened)
		assert.Equal(t, 11, res.ByClass[ProviderClassGoogle].Clicked)

		// outlook -> microsoft
		assert.Equal(t, 50, res.ByClass[ProviderClassMicrosoft].Sent)
		assert.Equal(t, 2, res.ByClass[ProviderClassMicrosoft].Bounced)

		// unknown suffix -> corporate (graceful MX degradation)
		assert.Equal(t, 7, res.ByClass[ProviderClassCorporate].Sent)

		// total is the sum across all rows
		assert.Equal(t, 167, res.Total.Sent)
		assert.Equal(t, 158, res.Total.Delivered)
		assert.Equal(t, 7, res.Total.Bounced)
		assert.Equal(t, 66, res.Total.Opened)
		assert.Equal(t, 16, res.Total.Clicked)
	})

	t.Run("empty domain falls into corporate, no panic", func(t *testing.T) {
		rows := []VeridianDomainEngagementRow{{Domain: "", Sent: 5}}
		res := VeridianAggregateEngagementByClass(rows)
		assert.Equal(t, 5, res.ByClass[ProviderClassCorporate].Sent)
		assert.Equal(t, 5, res.Total.Sent)
	})
}
