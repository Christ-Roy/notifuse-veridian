package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Les routes d'identité du Hub (§3.8) sont câblées au socle, avec la colonne V71 qu'elles écrivent.
func TestVeridianModHubUsers_CapaciteEtMigrationEnregistrees(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "hubusers")

	found := false
	for _, s := range veridianmig.Default.List() {
		if s.Number == 71 {
			found = true
		}
	}
	assert.True(t, found, "V71 enregistrée")
}
