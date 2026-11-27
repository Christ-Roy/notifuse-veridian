package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/veridian/leader"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Assemblage sans base réelle (tests, démo) : un seul exemplaire possible, il mène toujours ; aucun worker n'est exigé.
func TestVeridianFollowLeader_SansBaseMeneToujours(t *testing.T) {
	a := &App{veridianState: veridianState{veridianMockDB: true}, logger: logger.NewLoggerWithLevel("disabled")}
	g := a.veridianFollowLeader()
	_, always := g.(leader.Always)
	assert.True(t, always)
}
