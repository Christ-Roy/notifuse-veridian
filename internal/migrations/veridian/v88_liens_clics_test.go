package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV88_LiensClics_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 88 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V88 doit etre enregistree")
	require.Len(t, found.Workspace, 1)
	assert.Empty(t, found.System)
	assert.NoError(t, CheckExpandOnly(found.Workspace[0]))
}
