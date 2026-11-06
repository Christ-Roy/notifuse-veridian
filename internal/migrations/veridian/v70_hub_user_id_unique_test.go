package veridian

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV70_HubUserIDUnique_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 70 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V70 doit etre enregistree")
	assert.False(t, found.Contract)
	assert.Empty(t, found.Workspace, "users vit dans la base systeme")
	require.Len(t, found.System, 1)
	assert.Contains(t, found.System[0], "CREATE UNIQUE INDEX IF NOT EXISTS idx_users_hub_user_id_unique")
	assert.Contains(t, found.System[0], "WHERE hub_user_id IS NOT NULL", "les NULL legacy ne doivent pas se heurter")
	assert.NoError(t, CheckExpandOnly(found.System[0]))
}
