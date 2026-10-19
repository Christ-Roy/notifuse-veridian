package queue

import (
	"os"
	"testing"
)

// TestMain coupe les défauts de classe pour la suite historique (cf. veridian_class_defaults.go).
// Les tests du chemin par défaut appellent enableVeridianClassDefaults.
func TestMain(m *testing.M) {
	veridianClassDefaultsEnabled = false
	os.Exit(m.Run())
}

func enableVeridianClassDefaults(t *testing.T) {
	t.Helper()
	veridianClassDefaultsEnabled = true
	t.Cleanup(func() { veridianClassDefaultsEnabled = false })
}
