package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV82_AdminPlateforme_Additive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 82 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found)
	require.Len(t, found.System, 1)
	assert.Empty(t, found.Workspace)
	assert.NoError(t, CheckExpandOnly(found.System[0]))
}
