package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceVeridianMarketingEmailProfiles(t *testing.T) {
	provider := func(id string) Integration {
		verified := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		return Integration{ID: id, Type: IntegrationTypeEmail, EmailProvider: EmailProvider{Kind: EmailProviderKindSMTP, VeridianTransportVerifiedAt: &verified}}
	}

	t.Run("explicit arbitrary pool preserves order and drops invalid duplicates", func(t *testing.T) {
		legacy := provider("legacy")
		legacy.EmailProvider.VeridianTransportVerifiedAt = nil
		w := &Workspace{
			Settings:     WorkspaceSettings{MarketingEmailProviderID: "legacy", VeridianMarketingEmailProviderIDs: []string{"p2", "missing", "p1", "p2"}},
			Integrations: []Integration{legacy, provider("p1"), provider("p2")},
		}
		profiles := w.VeridianMarketingEmailProfiles()
		require.Len(t, profiles, 2)
		assert.Equal(t, "p2", profiles[0].IntegrationID)
		assert.Equal(t, "p1", profiles[1].IntegrationID)
	})

	t.Run("legacy singleton remains the fallback", func(t *testing.T) {
		w := &Workspace{Settings: WorkspaceSettings{MarketingEmailProviderID: "legacy"}, Integrations: []Integration{provider("legacy")}}
		profiles := w.VeridianMarketingEmailProfiles()
		require.Len(t, profiles, 1)
		assert.Equal(t, "legacy", profiles[0].IntegrationID)
	})
}

func TestWorkspaceValidateVeridianMarketingEmailProfiles(t *testing.T) {
	provider := func(id string) Integration {
		verified := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		return Integration{ID: id, Type: IntegrationTypeEmail, EmailProvider: EmailProvider{Kind: EmailProviderKindSMTP, VeridianTransportVerifiedAt: &verified}}
	}
	w := &Workspace{Settings: WorkspaceSettings{VeridianMarketingEmailProviderIDs: []string{"p1", "p2"}}, Integrations: []Integration{provider("p1"), provider("p2")}}
	assert.NoError(t, w.ValidateVeridianMarketingEmailProfiles())
	w.Settings.VeridianMarketingEmailProviderIDs = []string{"p1", "missing"}
	assert.ErrorContains(t, w.ValidateVeridianMarketingEmailProfiles(), "not found")
	w.Settings.VeridianMarketingEmailProviderIDs = []string{"p1", "p1"}
	assert.ErrorContains(t, w.ValidateVeridianMarketingEmailProfiles(), "duplicate")
	w.Settings.VeridianMarketingEmailProviderIDs = []string{"p1"}
	w.Settings.MarketingEmailProviderID = ""
	w.Integrations[0].EmailProvider.VeridianTransportVerifiedAt = nil
	assert.ErrorContains(t, w.ValidateVeridianMarketingEmailProfiles(), "not verified")

	legacy := provider("legacy")
	legacy.EmailProvider.VeridianTransportVerifiedAt = nil
	w = &Workspace{
		Settings:     WorkspaceSettings{MarketingEmailProviderID: "legacy", VeridianMarketingEmailProviderIDs: []string{"legacy", "p2"}},
		Integrations: []Integration{legacy, provider("p2")},
	}
	assert.NoError(t, w.ValidateVeridianMarketingEmailProfiles(), "active legacy singleton is grandfathered")
	assert.Len(t, w.VeridianMarketingEmailProfiles(), 2)
	w.Integrations[1].EmailProvider.VeridianTransportVerifiedAt = nil
	assert.ErrorContains(t, w.ValidateVeridianMarketingEmailProfiles(), "not verified", "new profiles remain gated")
}
