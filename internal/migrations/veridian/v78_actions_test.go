package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV78_Actions_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 78 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V78 doit etre enregistree")
	assert.Empty(t, found.Workspace)
	for _, q := range found.System {
		assert.NoError(t, CheckExpandOnly(q))
	}
	assert.Contains(t, found.System[0], "jti     VARCHAR(128) PRIMARY KEY")
	assert.Contains(t, found.System[2], "PRIMARY KEY (workspace_id, app, idem_key)")
}
