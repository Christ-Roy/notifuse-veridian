package domain

import (
	"encoding/json"
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

// --- Lot 2 « vérité d'un profil » : usage exclusif et lien IMAP ---

func lot2EmailIntegration(id string) Integration {
	return Integration{ID: id, Type: IntegrationTypeEmail, EmailProvider: EmailProvider{Kind: EmailProviderKindSMTP}}
}

func lot2IMAPIntegration(id string) Integration {
	return Integration{ID: id, Type: IntegrationTypeIMAP, IMAPSettings: &IMAPSettings{Host: "imap.example.test", Username: "retour@example.test"}}
}

func TestWorkspaceVeridianProfileUsageOf(t *testing.T) {
	w := &Workspace{
		Settings:     WorkspaceSettings{TransactionalEmailProviderID: "tx", VeridianMarketingEmailProviderIDs: []string{"c1", "c2"}},
		Integrations: []Integration{lot2EmailIntegration("c1"), lot2EmailIntegration("c2"), lot2EmailIntegration("tx"), lot2EmailIntegration("idle")},
	}
	assert.Equal(t, VeridianProfileUsageCommercial, w.VeridianProfileUsageOf("c1"))
	assert.Equal(t, VeridianProfileUsageCommercial, w.VeridianProfileUsageOf("c2"))
	assert.Equal(t, VeridianProfileUsageTransactional, w.VeridianProfileUsageOf("tx"))
	assert.Equal(t, VeridianProfileUsageUnassigned, w.VeridianProfileUsageOf("idle"))
	assert.Equal(t, VeridianProfileUsageUnassigned, w.VeridianProfileUsageOf(""))

	t.Run("legacy singleton counts as commercial when no explicit pool", func(t *testing.T) {
		legacy := &Workspace{Settings: WorkspaceSettings{MarketingEmailProviderID: "m"}, Integrations: []Integration{lot2EmailIntegration("m")}}
		assert.Equal(t, VeridianProfileUsageCommercial, legacy.VeridianProfileUsageOf("m"))
	})
	t.Run("a violating profile is reported commercial (it engages the reputation)", func(t *testing.T) {
		both := &Workspace{Settings: WorkspaceSettings{TransactionalEmailProviderID: "x", VeridianMarketingEmailProviderIDs: []string{"x"}}, Integrations: []Integration{lot2EmailIntegration("x")}}
		assert.Equal(t, VeridianProfileUsageCommercial, both.VeridianProfileUsageOf("x"))
	})
	var nilWorkspace *Workspace
	assert.Equal(t, VeridianProfileUsageUnassigned, nilWorkspace.VeridianProfileUsageOf("c1"))
}

func TestWorkspaceVeridianUsageConflictsAndExclusivity(t *testing.T) {
	ok := &Workspace{Settings: WorkspaceSettings{TransactionalEmailProviderID: "tx", VeridianMarketingEmailProviderIDs: []string{"c1"}}}
	assert.Empty(t, ok.VeridianUsageConflicts())
	assert.NoError(t, ok.ValidateVeridianUsageExclusivity())

	bad := &Workspace{Settings: WorkspaceSettings{TransactionalEmailProviderID: "c1", VeridianMarketingEmailProviderIDs: []string{"c0", "c1"}}}
	assert.Equal(t, []string{"c1"}, bad.VeridianUsageConflicts())
	err := bad.ValidateVeridianUsageExclusivity()
	require.Error(t, err)
	var validation ValidationError
	require.ErrorAs(t, err, &validation, "doit être une ValidationError pour sortir en 400")
	assert.Contains(t, validation.Message, "c1")
	assert.Contains(t, validation.Message, "both in the commercial rotation pool and the transactional profile")

	var nilWorkspace *Workspace
	assert.Empty(t, nilWorkspace.VeridianUsageConflicts())
	assert.NoError(t, (&Workspace{}).ValidateVeridianUsageExclusivity())
}

func TestWorkspaceValidateVeridianReturnIMAPLink(t *testing.T) {
	w := &Workspace{Integrations: []Integration{lot2EmailIntegration("p"), lot2IMAPIntegration("imap")}}

	assert.NoError(t, w.ValidateVeridianReturnIMAPLink(&EmailProvider{}), "pas de lien = valide")
	assert.NoError(t, w.ValidateVeridianReturnIMAPLink(nil))
	assert.NoError(t, w.ValidateVeridianReturnIMAPLink(&EmailProvider{VeridianReturnIMAPIntegrationID: "imap"}))

	for _, target := range []string{"absent", "p"} {
		err := w.ValidateVeridianReturnIMAPLink(&EmailProvider{VeridianReturnIMAPIntegrationID: target})
		require.Error(t, err, target)
		var validation ValidationError
		require.ErrorAs(t, err, &validation)
		assert.Contains(t, validation.Message, "imap integration of this workspace")
	}
}

func TestWorkspaceVeridianClearReturnIMAPLinks(t *testing.T) {
	linked := lot2EmailIntegration("p1")
	linked.EmailProvider.VeridianReturnIMAPIntegrationID = "imap-a"
	other := lot2EmailIntegration("p2")
	other.EmailProvider.VeridianReturnIMAPIntegrationID = "imap-b"
	w := &Workspace{Integrations: []Integration{linked, other}}
	w.VeridianClearReturnIMAPLinks("imap-a")
	assert.Empty(t, w.Integrations[0].EmailProvider.VeridianReturnIMAPIntegrationID)
	assert.Equal(t, "imap-b", w.Integrations[1].EmailProvider.VeridianReturnIMAPIntegrationID, "les autres liens ne bougent pas")
	w.VeridianClearReturnIMAPLinks("")
	var nilWorkspace *Workspace
	nilWorkspace.VeridianClearReturnIMAPLinks("imap-a")
}

func TestEmailProviderVeridianReturnIMAPAndPausedRoundTripJSON(t *testing.T) {
	in := EmailProvider{Kind: EmailProviderKindSMTP, VeridianReturnIMAPIntegrationID: "imap-1", VeridianPaused: true}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"veridian_return_imap_integration_id":"imap-1"`)
	assert.Contains(t, string(raw), `"veridian_paused":true`)
	var out EmailProvider
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "imap-1", out.VeridianReturnIMAPIntegrationID)
	assert.True(t, out.VeridianPaused)

	empty, err := json.Marshal(EmailProvider{Kind: EmailProviderKindSMTP})
	require.NoError(t, err)
	assert.NotContains(t, string(empty), "veridian_return_imap_integration_id", "omitempty : aucune migration, aucun bruit")
	assert.NotContains(t, string(empty), "veridian_paused")
}
