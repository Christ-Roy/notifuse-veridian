package veridian

import (
	"strings"
	"testing"
)

func okSpec(n int, lot string) Spec {
	return Spec{Number: n, Lot: lot, Name: "t", Workspace: []string{"CREATE TABLE IF NOT EXISTS t (id INT)"}}
}

func TestRegistry_Register(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string // sous-chaîne de l'erreur, "" = accepté
	}{
		{"ok", okSpec(63, "L1"), ""},
		{"numéro non réservé", okSpec(99, "L1"), "non réservé"},
		{"numéro déjà upstream", okSpec(62, "L1"), "non réservé"},
		{"mauvais lot", okSpec(64, "L1"), "appartient au lot L1b"},
		{"sans nom", Spec{Number: 65, Lot: "L2", Workspace: []string{"CREATE TABLE IF NOT EXISTS t (id INT)"}}, "nom vide"},
		{"vide", Spec{Number: 66, Lot: "L3", Name: "v"}, "migration vide"},
		{"destructif", Spec{Number: 67, Lot: "L4", Name: "d", Workspace: []string{"DROP TABLE t"}}, "CONTRACT"},
		{"ContractOf sans Contract", Spec{Number: 67, Lot: "L4", Name: "d", ContractOf: 63, Workspace: []string{"CREATE TABLE IF NOT EXISTS t (id INT)"}}, "ContractOf sans Contract"},
		{"contract sans expand", Spec{Number: 67, Lot: "L4", Name: "c", Contract: true, Workspace: []string{"DROP TABLE t"}}, "ContractOf"},
		{"contract expand absent", Spec{Number: 67, Lot: "L4", Name: "c", Contract: true, ContractOf: 66, Workspace: []string{"DROP TABLE t"}}, "pas enregistrée"},
	}
	for _, c := range cases {
		err := NewRegistry().Register(c.spec)
		if c.want == "" && err != nil {
			t.Errorf("%s: erreur inattendue %v", c.name, err)
		}
		if c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: erreur %v, attendu %q", c.name, err, c.want)
		}
	}
}

func TestRegistry_ContractAfterExpand(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(okSpec(63, "L1")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Spec{Number: 64, Lot: "L1b", Name: "c", Contract: true, ContractOf: 63, Workspace: []string{"ALTER TABLE t DROP COLUMN a"}}); err != nil {
		t.Fatalf("contract valide refusé: %v", err)
	}
}

func TestRegistry_DuplicateAndOrder(t *testing.T) {
	r := NewRegistry()
	for _, n := range []struct {
		n   int
		lot string
	}{{65, "L2"}, {63, "L1"}, {64, "L1b"}} {
		if err := r.Register(okSpec(n.n, n.lot)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Register(okSpec(63, "L1")); err == nil || !strings.Contains(err.Error(), "deux fois") {
		t.Fatalf("doublon non refusé: %v", err)
	}
	got := r.List()
	if len(got) != 3 || got[0].Number != 63 || got[1].Number != 64 || got[2].Number != 65 {
		t.Fatalf("ordre: %+v", got)
	}
}

func TestRegister_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register aurait dû paniquer")
		}
	}()
	Register(Spec{Number: 1, Lot: "X", Name: "n", Workspace: []string{"CREATE TABLE IF NOT EXISTS t (id INT)"}})
}

// Les migrations réellement enregistrées dans le processus respectent toutes les règles
// (la validation s'exécute dans init() ; ce test borne en plus le périmètre).
func TestDefaultRegistry_OnlyReservedNumbers(t *testing.T) {
	for _, s := range Default.List() {
		if Reserved[s.Number] != s.Lot {
			t.Errorf("V%d lot %q hors réservation", s.Number, s.Lot)
		}
	}
}
