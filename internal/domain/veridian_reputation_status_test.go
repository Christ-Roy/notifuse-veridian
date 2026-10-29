package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// L3 : chaque classe porte le seuil de ralentissement qui lui a été appliqué (profil, règle de la classe ou défaut du fusible) ;
// absent tant qu'il n'est pas calculé, pour ne pas changer la forme des anciennes réponses.
func TestVeridianReputationClassStatus_SeuilDeLaClasse(t *testing.T) {
	b, err := json.Marshal(VeridianReputationClassStatus{Class: "google", Threshold: 0.02})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"slowdown_threshold":0.02`)
	b, err = json.Marshal(VeridianReputationClassStatus{Class: "google"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "slowdown_threshold")
}
