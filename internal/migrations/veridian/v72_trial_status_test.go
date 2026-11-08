package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV72_TrialStatus_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 72 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V72 doit etre enregistree")
	assert.False(t, found.Contract)
	assert.Empty(t, found.Workspace, "veridian_plan vit dans la base systeme")
	require.Len(t, found.System, 2)
	for _, stmt := range found.System {
		assert.Contains(t, stmt, "ADD COLUMN IF NOT EXISTS")
		assert.NotContains(t, stmt, "NOT NULL", "nullable : le tag precedent doit tourner sur ce schema")
		assert.NoError(t, CheckExpandOnly(stmt))
	}
}
