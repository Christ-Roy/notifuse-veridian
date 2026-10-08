package service

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

func routeTestWorkspace(reserve bool) *domain.Workspace {
	mk := func(id string) domain.Integration {
		return domain.Integration{ID: id, Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}}
	}
	ws := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{mk("nord"), mk("tx")},
		Settings: domain.WorkspaceSettings{VeridianMarketingEmailProviderIDs: []string{"nord"}, MarketingEmailProviderID: "nord"}}
	if reserve {
		ws.Settings.TransactionalEmailProviderID = "tx"
	}
	return ws
}

func TestRouteTransactionalEmailNode(t *testing.T) {
	ws := routeTestWorkspace(true)
	nord := &ws.Integrations[0].EmailProvider

	provider, id, flagged := veridianRouteTransactionalEmailNode(ws, "transactional", nord, "nord")
	assert.True(t, flagged)
	assert.Equal(t, "tx", id, "un modele transactionnel part par le profil transactionnel reserve")
	assert.Same(t, &ws.Integrations[1].EmailProvider, provider)

	provider, id, flagged = veridianRouteTransactionalEmailNode(ws, "marketing", nord, "nord")
	assert.False(t, flagged)
	assert.Equal(t, "nord", id)
	assert.Same(t, nord, provider)

	// Sans profil reserve : comportement natif, le modele transactionnel suit le noeud.
	native := routeTestWorkspace(false)
	provider, id, flagged = veridianRouteTransactionalEmailNode(native, "transactional", &native.Integrations[0].EmailProvider, "nord")
	assert.False(t, flagged)
	assert.Equal(t, "nord", id)
	assert.Same(t, &native.Integrations[0].EmailProvider, provider)

	// Workspace purement transactionnel : aucun profil marketing, le modele part quand meme.
	only := routeTestWorkspace(true)
	only.Settings.VeridianMarketingEmailProviderIDs = nil
	only.Settings.MarketingEmailProviderID = ""
	provider, id, flagged = veridianRouteTransactionalEmailNode(only, "transactional", nil, "")
	assert.True(t, flagged)
	assert.Equal(t, "tx", id)
	assert.NotNil(t, provider)
}
