package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV64ReconcileIsRegisteredAndExpandOnly(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 64 {
			s := s
			found = &s
		}
	}
	require.NotNil(t, found, "V64 enregistrée au chargement du paquet")
	assert.Equal(t, "L1b", found.Lot)
	assert.NotEmpty(t, found.Workspace)
	assert.Empty(t, found.System, "la table des écarts vit dans chaque base workspace, pas dans la base système")
	for _, stmt := range found.Workspace {
		assert.NoError(t, CheckExpandOnly(stmt), stmt)
	}
	// Le contrôle d'idempotence est réel : un CREATE TABLE nu serait refusé.
	assert.Error(t, CheckExpandOnly("CREATE TABLE veridian_reconcile_finding (id TEXT)"))
}
