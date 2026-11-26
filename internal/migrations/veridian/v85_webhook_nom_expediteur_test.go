package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV85_WebhookNomExpediteur_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 85 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V85 doit etre enregistree")
	require.Len(t, found.Workspace, 1)
	assert.NoError(t, CheckExpandOnly(found.Workspace[0]))
	assert.Contains(t, found.Workspace[0], "'from_name', NEW.from_name")
}
