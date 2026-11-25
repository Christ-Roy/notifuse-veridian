package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// La capacité, sa migration, et le dépôt RÉEL qui porte le registre : sans lui, les gardes de retrait se tairaient (fiche 95).
func TestVeridianModWsAdmin_CapaciteMigrationEtRegistre(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "wsadmin")
	found := false
	for _, s := range veridianmig.Default.List() {
		found = found || s.Number == 82
	}
	assert.True(t, found, "V82 enregistrée")
	_, ok := repository.NewWorkspaceRepository(nil, &config.DatabaseConfig{}, "", nil).(domain.VeridianAdminLedger)
	assert.True(t, ok)
}
