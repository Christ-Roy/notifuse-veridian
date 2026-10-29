package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendPriority_Validation(t *testing.T) {
	for _, ok := range []int{2, 5, 9} {
		assert.NoError(t, VeridianValidateMarketingPriority(ok), ok)
	}
	for _, ko := range []int{-1, 0, 1, 10, 100} {
		err := VeridianValidateMarketingPriority(ko)
		require.Error(t, err, ko)
		assert.True(t, strings.Contains(err.Error(), "2 et 9"), err.Error())
	}
}

type fakePrioProvider struct{ p int }

func (f fakePrioProvider) SendPriorityFor(string, EmailQueueSourceType, string) int { return f.p }

func TestSendPriority_DefautCinqEtGardeFouDePlage(t *testing.T) {
	t.Cleanup(func() { SetVeridianSendPriorityProvider(nil) })
	SetVeridianSendPriorityProvider(nil)
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "sans fournisseur : 5")
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 0})
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "0 = non posé : 5")
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 2})
	assert.Equal(t, 2, VeridianSendPriority("ws", EmailQueueSourceAutomation, "a1"))
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 1})
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "1 est réservé au transactionnel : jamais atteint par du marketing")
	SetVeridianSendPriorityProvider(fakePrioProvider{p: 42})
	assert.Equal(t, 5, VeridianSendPriority("ws", EmailQueueSourceBroadcast, "b1"), "donnée corrompue : 5")
}
