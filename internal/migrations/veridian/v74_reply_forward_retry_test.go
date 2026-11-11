package veridian

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestV74_ReplyForwardRetry_RegistreeEtAdditive(t *testing.T) {
	var found *Spec
	for _, s := range Default.List() {
		if s.Number == 74 {
			sp := s
			found = &sp
		}
	}
	require.NotNil(t, found, "V74 doit etre enregistree")
	assert.Equal(t, "L1d", found.Lot)
	assert.False(t, found.Contract)
	assert.Empty(t, found.System)
	require.Len(t, found.Workspace, 4)
	joined := ""
	for _, ddl := range found.Workspace {
		assert.NoError(t, CheckExpandOnly(ddl), ddl)
		assert.Contains(t, ddl, "ALTER TABLE veridian_reply_forward ADD COLUMN IF NOT EXISTS")
		assert.NotContains(t, strings.ToUpper(ddl), "NOT NULL", "colonnes NULLABLES : le tag precedent continue d'inserer sans elles")
		for _, banned := range []string{"DROP ", "TRUNCATE", "RENAME ", "CREATE INDEX"} {
			assert.False(t, strings.Contains(strings.ToUpper(ddl), banned), "instruction non additive : %s", banned)
		}
		joined += ddl + "\n"
	}
	for _, col := range []string{"payload", "attempts", "next_attempt_at", "slot_at"} {
		assert.Contains(t, joined, col)
	}
}
