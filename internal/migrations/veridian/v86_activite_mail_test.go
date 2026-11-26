package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV86_ActiviteMail_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 86 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V86 doit etre enregistree")
	require.NotEmpty(t, found.Workspace)
	for _, st := range found.Workspace {
		assert.NoError(t, CheckExpandOnly(st))
	}
}
