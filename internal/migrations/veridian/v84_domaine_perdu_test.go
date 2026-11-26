package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV84_DomainePerdu_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 84 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V84 doit etre enregistree")
	require.Len(t, found.System, 2)
	assert.Empty(t, found.Workspace)
	for _, q := range found.System {
		assert.NoError(t, CheckExpandOnly(q))
	}
}
