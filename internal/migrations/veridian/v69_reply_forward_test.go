package veridian

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV69_ReplyForward_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 69 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V69 doit etre enregistree")
	assert.Equal(t, "L1d", found.Lot)
	assert.False(t, found.Contract)
	require.Len(t, found.Workspace, 1)
	assert.Empty(t, found.System)
	ddl := found.Workspace[0]
	assert.Contains(t, ddl, "CREATE TABLE IF NOT EXISTS veridian_reply_forward")
	assert.Contains(t, ddl, "reply_key   VARCHAR(64) PRIMARY KEY")
	for _, banned := range []string{"DROP ", "TRUNCATE", "RENAME ", "CONCURRENTLY", "ALTER ", "CREATE INDEX"} {
		assert.False(t, strings.Contains(strings.ToUpper(ddl), banned), "instruction non additive : %s", banned)
	}
	// Aucune colonne qui pourrait porter une donnee personnelle.
	for _, pii := range []string{"email", "subject", "body", "address"} {
		assert.NotContains(t, strings.ToLower(ddl), pii)
	}
	assert.NoError(t, CheckExpandOnly(ddl))
}
