package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV73_HubTenantID_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 73 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V73 doit etre enregistree")
	assert.False(t, found.Contract)
	assert.Empty(t, found.Workspace)
	require.Len(t, found.System, 2)
	assert.Contains(t, found.System[0], "ADD COLUMN IF NOT EXISTS hub_tenant_id")
	assert.NotContains(t, found.System[0], "NOT NULL")
	for _, stmt := range found.System {
		assert.NoError(t, CheckExpandOnly(stmt))
	}
}
