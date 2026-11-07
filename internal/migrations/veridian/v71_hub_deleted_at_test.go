package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV71_HubDeletedAt_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 71 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V71 doit etre enregistree")
	assert.False(t, found.Contract)
	require.Len(t, found.System, 1)
	assert.Contains(t, found.System[0], "ADD COLUMN IF NOT EXISTS hub_deleted_at TIMESTAMPTZ NULL")
	assert.NoError(t, CheckExpandOnly(found.System[0]))
}
