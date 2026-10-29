package mailbox

import (
	"strings"
	"testing"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
)

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

// La migration V63 porte une copie littérale du schéma : elle ne doit jamais diverger.
func TestSchemaMatchesMigrationV63(t *testing.T) {
	var spec *veridianmig.Spec
	for _, s := range veridianmig.Default.List() {
		if s.Number == 63 {
			s := s
			spec = &s
		}
	}
	if spec == nil {
		t.Fatal("la migration V63 n'est pas enregistrée")
	}
	if len(spec.Workspace) != len(SchemaStatements) {
		t.Fatalf("%d instructions dans la migration, %d dans SchemaStatements", len(spec.Workspace), len(SchemaStatements))
	}
	for i := range SchemaStatements {
		if norm(spec.Workspace[i]) != norm(SchemaStatements[i]) {
			t.Fatalf("instruction %d divergente:\nmigration: %s\nschéma:    %s", i, norm(spec.Workspace[i]), norm(SchemaStatements[i]))
		}
	}
}
