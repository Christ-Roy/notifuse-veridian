package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianAggregateEngagementByClass(t *testing.T) {
	t.Run("empty rows -> all canonical classes + unclassified at zero", func(t *testing.T) {
		res := VeridianAggregateEngagementByClass(nil)
		require.NotNil(t, res)
		for _, class := range VeridianAllProviderClasses() {
			agg, ok := res.ByClass[class]
			require.True(t, ok, "class %s must be present", class)
			assert.Equal(t, VeridianClassEngagement{}, agg)
		}
		_, ok := res.ByClass[VeridianUnclassified]
		assert.True(t, ok)
		assert.Equal(t, VeridianClassEngagement{}, res.Total)
	})

	t.Run("aggregates per persisted class (ovh, ionos... no longer collapsed into corporate)", func(t *testing.T) {
		rows := []VeridianClassEngagementRow{
			{Class: ProviderClassOVH, Sent: 235, Bounced: 14, RepliedHuman: 4},
			{Class: ProviderClassIonos, Sent: 70, Bounced: 5},
			{Class: ProviderClassSecurityGateway, Sent: 109, Bounced: 9, RepliedHuman: 1},
			{Class: ProviderClassOVH, Sent: 5, Bounced: 1},
		}
		res := VeridianAggregateEngagementByClass(rows)
		assert.Equal(t, 240, res.ByClass[ProviderClassOVH].Sent)
		assert.Equal(t, 15, res.ByClass[ProviderClassOVH].Bounced)
		assert.Equal(t, 4, res.ByClass[ProviderClassOVH].RepliedHuman)
		assert.Equal(t, 70, res.ByClass[ProviderClassIonos].Sent)
		assert.Equal(t, 0, res.ByClass[ProviderClassCorporate].Sent)
		assert.Equal(t, 419, res.Total.Sent)
		assert.Equal(t, 29, res.Total.Bounced)
		assert.Equal(t, 5, res.Total.RepliedHuman)
	})

	t.Run("empty or unknown class -> unclassified, no panic", func(t *testing.T) {
		rows := []VeridianClassEngagementRow{{Class: "", Sent: 10}, {Class: "weird", Sent: 2}}
		res := VeridianAggregateEngagementByClass(rows)
		assert.Equal(t, 12, res.ByClass[VeridianUnclassified].Sent)
		assert.Equal(t, 12, res.Total.Sent)
	})
}
