package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

// Le plafond d'un FAI est le sien : 10 envois chez Orange n'entament pas celui de Free,
// y compris pour des entrées étiquetées freemail_fr.
func TestVeridianDailyCapGate_LegacyTaggedEntriesCountPerFAI(t *testing.T) {
	enableVeridianClassDefaults(t)
	worker, fakeRepo := newVeridianDailyCapFakeEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)

	capOrange := domain.VeridianDefaultCapForClass(domain.ProviderClassOrange)
	capFree := domain.VeridianDefaultCapForClass(domain.ProviderClassFree)
	assert.Less(t, capOrange, capFree, "Orange, le plus sévère, a le plafond le plus bas")

	send := func(id, email string) bool {
		entry := veridianTestEntryFrom(id, email, "bot@send.fr",
			domain.EmailQueuePayload{VeridianProviderClass: domain.ProviderClassFreemailFR})
		// Le worker fige la classe avant les portes (worker.go) : même geste.
		entry.Payload.VeridianProviderClass = worker.veridianClassifyRecipient(entry)
		if _, capped := worker.veridianDailyCapGate(ws, nil, entry); capped {
			return false
		}
		fakeRepo.record(entry.Payload.VeridianProviderClass, "send.fr", entry.ContactEmail, time.Now().UTC())
		return true
	}
	for i := 0; i < capOrange; i++ {
		assert.True(t, send(fmt.Sprintf("o%d", i), fmt.Sprintf("o%d@orange.fr", i)), "orange %d", i)
	}
	assert.False(t, send("o-over", "over@orange.fr"), "Orange plafonné à %d", capOrange)
	assert.True(t, send("f1", "f1@free.fr"), "Free n'est pas ralenti par Orange")
	assert.True(t, send("s1", "s1@sfr.fr"), "SFR n'est pas ralenti par Orange")
}
