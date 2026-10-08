
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianIsTransactionalCategory(t *testing.T) {
	assert.True(t, VeridianIsTransactionalCategory("transactional"))
	for _, c := range []string{"marketing", "welcome", "opt_in", "unsubscribe", "blocklist", "other", ""} {
		assert.False(t, VeridianIsTransactionalCategory(c), c)
	}
	assert.Equal(t, "transactional", VeridianMessageTypeTransactional)
	assert.Equal(t, "commercial", VeridianMessageTypeCommercial)
}
