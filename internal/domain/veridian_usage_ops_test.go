package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func usageTestProfile(id string, verified bool) Integration {
	p := EmailProvider{Kind: EmailProviderKindSMTP, SMTP: &SMTPSettings{Host: "smtp." + id + ".example", Port: 587},
		Senders: []EmailSender{{ID: "s-" + id, Email: id + "@" + id + ".example", IsDefault: true}}}
	if verified {
		now := time.Now()
		p.VeridianTransportVerifiedAt = &now
	}
	return Integration{ID: id, Name: id, Type: IntegrationTypeEmail, EmailProvider: p}
}

func usageTestWorkspace(pool []string, marketing, transactional string, ids ...string) *Workspace {
	ws := &Workspace{ID: "ws"}
	for _, id := range ids {
		ws.Integrations = append(ws.Integrations, usageTestProfile(id, true))
	}
	ws.Settings.VeridianMarketingEmailProviderIDs = pool
	ws.Settings.MarketingEmailProviderID = marketing
	ws.Settings.TransactionalEmailProviderID = transactional
	return ws
}

func TestReservedTransactionalProfile(t *testing.T) {
	assert.Equal(t, "tx", usageTestWorkspace([]string{"a"}, "a", "tx", "a", "tx").VeridianReservedTransactionalProfileID())
	assert.Equal(t, "", usageTestWorkspace([]string{"a"}, "a", "", "a", "tx").VeridianReservedTransactionalProfileID(), "pas de profil transactionnel")
	assert.Equal(t, "", usageTestWorkspace([]string{"a"}, "a", "ghost", "a").VeridianReservedTransactionalProfileID(), "profil inconnu")
	assert.Equal(t, "", usageTestWorkspace([]string{"a", "tx"}, "a", "tx", "a", "tx").VeridianReservedTransactionalProfileID(), "dans le pool : violation, comportement inchange")
	assert.Equal(t, "", usageTestWorkspace(nil, "one", "one", "one").VeridianReservedTransactionalProfileID(), "singleton natif partage")
	assert.Equal(t, "tx", usageTestWorkspace([]string{"a"}, "tx", "tx", "a", "tx").VeridianReservedTransactionalProfileID(), "pool explicite sans lui : reserve")
}

func TestApplyUsage_TransactionalLeavesRotationAndReplacesPrevious(t *testing.T) {
	ws := usageTestWorkspace([]string{"a", "b"}, "a", "old", "a", "b", "old", "new")
	res, err := ws.VeridianApplyUsage("new", VeridianUsageRequestTransactional)
	require.NoError(t, err)
	assert.Equal(t, "new", ws.Settings.TransactionalEmailProviderID)
	assert.Equal(t, "old", res.PreviousTransactionalIntegrationID)
	assert.Equal(t, VeridianProfileUsageTransactional, res.Usage)
	assert.Equal(t, []string{"a", "b"}, res.Rotation)
	assert.Equal(t, VeridianProfileUsageUnassigned, ws.VeridianProfileUsageOf("old"))
}

func TestApplyUsage_CommercialProfileBecomingTransactionalLeavesPoolAndFixesMarketingDefault(t *testing.T) {
	ws := usageTestWorkspace([]string{"a", "b"}, "a", "", "a", "b")
	res, err := ws.VeridianApplyUsage("a", VeridianUsageRequestTransactional)
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, ws.Settings.VeridianMarketingEmailProviderIDs)
	assert.Equal(t, "b", ws.Settings.MarketingEmailProviderID, "le profil marketing par defaut reste membre de la rotation")
	assert.False(t, res.InRotation)
	assert.Equal(t, "a", ws.Settings.TransactionalEmailProviderID)
}

func TestApplyUsage_RefusesToEmptyTheCommercialRotation(t *testing.T) {
	ws := usageTestWorkspace([]string{"a"}, "a", "", "a", "tx")
	_, err := ws.VeridianApplyUsage("a", VeridianUsageRequestTransactional)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty the commercial rotation")
	_, err = ws.VeridianApplyUsage("a", VeridianUsageRequestUnassigned)
	require.Error(t, err)
	// Le singleton natif est aussi une rotation d'un membre.
	single := usageTestWorkspace(nil, "one", "", "one")
	_, err = single.VeridianApplyUsage("one", VeridianUsageRequestTransactional)
	require.Error(t, err)
}

func TestApplyUsage_CommercialNeedsVerifiedProfileAndKeepsSingletonInTheNewPool(t *testing.T) {
	ws := usageTestWorkspace(nil, "one", "", "one")
	ws.Integrations = append(ws.Integrations, usageTestProfile("raw", false), usageTestProfile("ok", true))
	_, err := ws.VeridianApplyUsage("raw", VeridianUsageRequestCommercial)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not verified")

	res, err := ws.VeridianApplyUsage("ok", VeridianUsageRequestCommercial)
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "ok"}, ws.Settings.VeridianMarketingEmailProviderIDs, "le singleton historique reste le premier membre de la rotation nee de lui")
	assert.Equal(t, "one", ws.Settings.MarketingEmailProviderID)
	assert.True(t, res.InRotation)
}

func TestApplyUsage_CommercialClearsTransactionalAndFirstProfileBecomesDefault(t *testing.T) {
	ws := usageTestWorkspace(nil, "", "tx", "tx", "a")
	res, err := ws.VeridianApplyUsage("tx", VeridianUsageRequestCommercial)
	require.NoError(t, err)
	assert.Equal(t, "", ws.Settings.TransactionalEmailProviderID)
	assert.Equal(t, "tx", ws.Settings.MarketingEmailProviderID)
	assert.Equal(t, VeridianProfileUsageCommercial, res.Usage)
}

func TestApplyUsage_SingletonDualIsSplitWhenACommercialProfileJoins(t *testing.T) {
	ws := usageTestWorkspace(nil, "one", "one", "one", "a")
	_, err := ws.VeridianApplyUsage("a", VeridianUsageRequestCommercial)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ws.Settings.VeridianMarketingEmailProviderIDs)
	assert.Equal(t, "a", ws.Settings.MarketingEmailProviderID)
	assert.Equal(t, "one", ws.VeridianReservedTransactionalProfileID(), "le profil partage devient le transactionnel reserve")
}

func TestApplyUsage_TransactionalClearsItsPauseAndUnknownProfileIsRefused(t *testing.T) {
	ws := usageTestWorkspace([]string{"a", "b"}, "a", "", "a", "b")
	ws.Integrations[1].EmailProvider.VeridianPaused = true
	res, err := ws.VeridianApplyUsage("b", VeridianUsageRequestTransactional)
	require.NoError(t, err)
	assert.True(t, res.ClearedPause)
	assert.False(t, ws.GetIntegrationByID("b").EmailProvider.VeridianPaused)
	_, err = ws.VeridianApplyUsage("ghost", VeridianUsageRequestCommercial)
	require.Error(t, err)
	_, err = ws.VeridianApplyUsage("a", "weird")
	require.Error(t, err)
}

func TestApplyPause_TransactionalProfileCannotBePaused(t *testing.T) {
	ws := usageTestWorkspace([]string{"a"}, "a", "tx", "a", "tx")
	_, err := ws.VeridianApplyPause("tx", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be paused")
	res, err := ws.VeridianApplyPause("a", true)
	require.NoError(t, err)
	assert.True(t, res.Paused)
	assert.True(t, ws.GetIntegrationByID("a").EmailProvider.VeridianPaused)
	_, err = ws.VeridianApplyPause("a", false)
	require.NoError(t, err)
	assert.False(t, ws.GetIntegrationByID("a").EmailProvider.VeridianPaused)
	// Lever la pause d'un transactionnel reste permis (nettoyage).
	_, err = ws.VeridianApplyPause("tx", false)
	require.NoError(t, err)
}

func TestSetUsageRequestValidate(t *testing.T) {
	assert.NoError(t, (&VeridianSetUsageRequest{WorkspaceID: "w", IntegrationID: "i", Usage: "commercial"}).Validate())
	assert.Error(t, (&VeridianSetUsageRequest{WorkspaceID: "w", IntegrationID: "i", Usage: "x"}).Validate())
	assert.Error(t, (&VeridianSetUsageRequest{IntegrationID: "i", Usage: "commercial"}).Validate())
	assert.Error(t, (&VeridianPauseRequest{WorkspaceID: "w"}).Validate())
}
