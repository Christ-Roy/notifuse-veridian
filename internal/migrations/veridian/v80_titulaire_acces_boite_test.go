package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Les accès d'avant V80 restent des accès d'espace : aucun rattachement après coup, colonne vide admise.
func TestV80_TitulaireAccesBoite_AdditiveEtSansRattachement(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 80 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V80 doit etre enregistree")
	require.Len(t, found.System, 1)
	assert.Empty(t, found.Workspace)
	assert.NoError(t, CheckExpandOnly(found.System[0]))
	assert.Contains(t, found.System[0], "UUID NULL")
	assert.NotContains(t, found.System[0], "UPDATE")
}
