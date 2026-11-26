package veridian

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV83_DomainesEnvoi_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 83 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V83 doit etre enregistree")
	require.Len(t, found.System, 2)
	assert.Empty(t, found.Workspace)
	for _, q := range found.System {
		assert.NoError(t, CheckExpandOnly(q))
	}
	// Une seule ligne par espace et par domaine : la signature à l'envoi ne doit jamais choisir entre deux clés.
	assert.True(t, strings.Contains(found.System[1], "UNIQUE INDEX") && strings.Contains(found.System[1], "(workspace_id, domain)"))
}
