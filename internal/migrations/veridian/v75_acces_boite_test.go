package veridian

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV75_AccesBoite_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 75 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V75 doit etre enregistree")
	assert.Equal(t, "Boites", found.Lot)
	assert.False(t, found.Contract)
	for _, stmt := range append(append([]string{}, found.System...), found.Workspace...) {
		assert.NoError(t, CheckExpandOnly(stmt))
		assert.NotContains(t, strings.ToLower(stmt), "password", "aucun mot de passe en base, seulement une empreinte")
	}
	assert.Contains(t, found.System[0], "secret_hash")
}
