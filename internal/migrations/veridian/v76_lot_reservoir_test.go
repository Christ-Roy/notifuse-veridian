package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV76_LotReservoir_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 76 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V76 doit etre enregistree")
	assert.Equal(t, "Reservoirs", found.Lot)
	assert.False(t, found.Contract)
	require.Len(t, found.Workspace, 1)
	assert.NoError(t, CheckExpandOnly(found.Workspace[0]))
	assert.Contains(t, found.Workspace[0], "PRIMARY KEY (automation_id, batch_id)")
}
