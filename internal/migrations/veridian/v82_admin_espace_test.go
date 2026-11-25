package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV82_AdminEspace_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 82 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V82 doit etre enregistree")
	require.Len(t, found.System, 3)
	assert.Empty(t, found.Workspace)
	for _, q := range found.System {
		assert.NoError(t, CheckExpandOnly(q))
	}
}
