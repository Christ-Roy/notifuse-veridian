package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// Le module s'accroche au socle : sans ce fichier d'import, les routes n'existeraient pas en production.
func TestVeridianModReservoir_CapaciteEnregistree(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "reservoir")
}
