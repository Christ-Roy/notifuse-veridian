package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV79_Organisation_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 79 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V79 doit etre enregistree")
	require.Len(t, found.System, 1)
	assert.Empty(t, found.Workspace)
	assert.NoError(t, CheckExpandOnly(found.System[0]))
	assert.Contains(t, found.System[0], "version       BIGINT NOT NULL")
}
