package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV77_WebhookMessageRecu_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 77 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V77 doit etre enregistree")
	require.Len(t, found.Workspace, 1)
	assert.NoError(t, CheckExpandOnly(found.Workspace[0]))
	for _, field := range []string{"rfc_message_id", "match_type", "NEW.message_id"} {
		assert.Contains(t, found.Workspace[0], field)
	}
}
