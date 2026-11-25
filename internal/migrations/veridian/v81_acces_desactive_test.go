package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV81_AccesDesactive_Additive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 81 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V81 doit etre enregistree")
	require.Len(t, found.System, 1)
	assert.NoError(t, CheckExpandOnly(found.System[0]))
	assert.Contains(t, found.System[0], "TIMESTAMPTZ NULL")
}
