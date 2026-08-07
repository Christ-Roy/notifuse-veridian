package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completeColdSafetySettings() WorkspaceSettings {
	return WorkspaceSettings{
		Timezone:                        "Europe/Paris",
		VeridianColdSafetyEnabled:       true,
		VeridianProviderClassRates:      map[string]float64{"ovh": 0.1},
		VeridianProviderClassDailyCap:   map[string]int{"ovh": 10},
		VeridianWorkspaceDailyCap:       20,
		VeridianPerSenderDailyCap:       15,
		VeridianRecipientDomainDailyCap: 2,
		VeridianPerRecipientDailyCap:    1,
	}
}

func TestColdSafetyPolicyIsOptIn(t *testing.T) {
	settings := WorkspaceSettings{Timezone: "Europe/Paris"}
	require.NoError(t, settings.ValidateVeridianColdSafety())
}

func TestColdSafetyPolicyAcceptsCompleteCaps(t *testing.T) {
	settings := completeColdSafetySettings()
	require.NoError(t, settings.ValidateVeridianColdSafety())
}

func TestColdSafetyPolicyRejectsEveryMissingCap(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkspaceSettings)
		error  string
	}{
		{"workspace", func(s *WorkspaceSettings) { s.VeridianWorkspaceDailyCap = 0 }, "workspace daily cap"},
		{"sender", func(s *WorkspaceSettings) { s.VeridianPerSenderDailyCap = 0 }, "per-sender daily cap"},
		{"domain", func(s *WorkspaceSettings) { s.VeridianRecipientDomainDailyCap = 0 }, "recipient-domain daily cap"},
		{"recipient", func(s *WorkspaceSettings) { s.VeridianPerRecipientDailyCap = 0 }, "per-recipient daily cap"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := completeColdSafetySettings()
			test.mutate(&settings)
			assert.ErrorContains(t, settings.ValidateVeridianColdSafety(), test.error)
		})
	}
}

func TestColdSafetyPolicyRejectsProviderCapWithoutRate(t *testing.T) {
	settings := completeColdSafetySettings()
	settings.VeridianProviderClassDailyCap["microsoft"] = 1
	assert.ErrorContains(t, settings.ValidateVeridianColdSafety(), "missing rate")
}

func TestColdSafetyPolicyRejectsContradictoryCaps(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkspaceSettings)
		error  string
	}{
		{"sender above workspace", func(s *WorkspaceSettings) { s.VeridianPerSenderDailyCap = 21 }, "per-sender daily cap cannot exceed"},
		{"domain above workspace", func(s *WorkspaceSettings) { s.VeridianRecipientDomainDailyCap = 21 }, "recipient-domain daily cap cannot exceed"},
		{"recipient above domain", func(s *WorkspaceSettings) { s.VeridianPerRecipientDailyCap = 3 }, "per-recipient daily cap cannot exceed"},
		{"provider above workspace", func(s *WorkspaceSettings) { s.VeridianProviderClassDailyCap["ovh"] = 21 }, "provider class \"ovh\" cannot exceed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := completeColdSafetySettings()
			test.mutate(&settings)
			assert.ErrorContains(t, settings.ValidateVeridianColdSafety(), test.error)
		})
	}
}

func TestColdSafetyPolicyRejectsMalformedExcludedProviderClasses(t *testing.T) {
	settings := completeColdSafetySettings()
	settings.VeridianExcludedProviderClasses = []string{" Google "}
	assert.ErrorContains(t, settings.ValidateVeridianColdSafety(), "must be normalized")

	settings.VeridianExcludedProviderClasses = []string{"google", "google"}
	assert.ErrorContains(t, settings.ValidateVeridianColdSafety(), "is duplicated")
}
