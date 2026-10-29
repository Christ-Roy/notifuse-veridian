package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Le module L3 s'accroche au socle : sa capacité est enregistrée et sa migration V66 aussi.
func TestVeridianModPolicy_CapaciteEtMigrationEnregistrees(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "policy")

	found := false
	for _, s := range veridianmig.Default.List() {
		if s.Number == 66 {
			found = true
			assert.Equal(t, "L3", s.Lot)
			assert.NotEmpty(t, s.Workspace, "V66 crée des tables dans la base de chaque workspace")
		}
	}
	assert.True(t, found, "V66 enregistrée")
}
