package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Le MCP de Notifuse est câblé au socle : la porte du Hub le trouve à /api/mcp.
func TestVeridianModMCP_CapaciteEnregistree(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "mcp")
}
