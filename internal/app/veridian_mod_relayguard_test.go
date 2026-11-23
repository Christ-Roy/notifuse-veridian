package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// VER-225 : l'application charge la porte « relais hors liste noire » (import à blanc).
func TestVeridianModRelayGuard_CapaciteEnregistree(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "relayguard")
}
