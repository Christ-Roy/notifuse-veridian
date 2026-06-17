package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianBounceTypeLabel(t *testing.T) {
	tests := []struct {
		name  string
		class BounceClassification
		want  string
	}{
		{"hard -> HardBounce", BounceClassificationHard, VeridianBounceTypeHard},
		{"soft count -> SoftBounce", BounceClassificationSoftCount, VeridianBounceTypeSoft},
		{"soft ignore -> SoftBounce", BounceClassificationSoftIgnore, VeridianBounceTypeSoft},
		{"unknown -> empty", BounceClassification(999), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianBounceTypeLabel(tt.class))
		})
	}
	// The persisted literals MUST match the analytics ILIKE filters
	// ('hard%' / 'soft%') declared in analytics.go count_bounced_hard/soft.
	assert.Equal(t, "HardBounce", VeridianBounceTypeHard)
	assert.Equal(t, "SoftBounce", VeridianBounceTypeSoft)
}
