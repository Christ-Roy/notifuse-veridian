package domain

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianHardBounceFreezeThreshold_ValidationBounds(t *testing.T) {
	cases := []struct {
		v  float64
		ok bool
	}{
		{0, true}, {0.01, true}, {0.03, true}, {0.08, true}, {0.15, true},
		{0.009, false}, {0.151, false}, {0.5, false}, {-0.03, false}, {math.NaN(), false}, {math.Inf(1), false},
	}
	for _, c := range cases {
		e := &EmailProvider{VeridianHardBounceFreezeThreshold: c.v}
		if c.ok {
			assert.NoError(t, e.ValidateVeridianHardBounceFreezeThreshold(), "v=%v", c.v)
		} else {
			assert.Error(t, e.ValidateVeridianHardBounceFreezeThreshold(), "v=%v", c.v)
		}
	}
}

func TestVeridianHardBounceFreezeThreshold_ValidateRejectsOutOfRange(t *testing.T) {
	e := &EmailProvider{Kind: EmailProviderKindSMTP, VeridianHardBounceFreezeThreshold: 0.5}
	assert.Error(t, e.Validate("pass"))
}

func TestVeridianHardBounceFreezeThreshold_EffectiveDefaults(t *testing.T) {
	var nilProvider *EmailProvider
	assert.Equal(t, 0.03, nilProvider.VeridianEffectiveHardBounceFreezeThreshold())
	assert.Equal(t, 0.03, (&EmailProvider{}).VeridianEffectiveHardBounceFreezeThreshold())
	assert.Equal(t, 0.08, (&EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}).VeridianEffectiveHardBounceFreezeThreshold())
	// valeur hors bornes dans le blob : retombe sur le defaut, jamais sur un fusible desarme
	assert.Equal(t, 0.03, (&EmailProvider{VeridianHardBounceFreezeThreshold: 0.9}).VeridianEffectiveHardBounceFreezeThreshold())
	assert.False(t, (&EmailProvider{}).VeridianHasCustomHardBounceFreezeThreshold())
	assert.True(t, (&EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}).VeridianHasCustomHardBounceFreezeThreshold())
}

func TestVeridianHardBounceFreezeThreshold_JSONOmitemptyAndRoundTrip(t *testing.T) {
	b, err := json.Marshal(EmailProvider{})
	assert.NoError(t, err)
	assert.NotContains(t, string(b), "veridian_hard_bounce_freeze_threshold")

	b, err = json.Marshal(EmailProvider{VeridianHardBounceFreezeThreshold: 0.08})
	assert.NoError(t, err)
	assert.Contains(t, string(b), `"veridian_hard_bounce_freeze_threshold":0.08`)

	var back EmailProvider
	assert.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, 0.08, back.VeridianHardBounceFreezeThreshold)
}
