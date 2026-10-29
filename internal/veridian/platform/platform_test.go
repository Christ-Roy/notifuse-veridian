package platform

import (
	"strings"
	"testing"
)

func wireOK(Host) error { return nil }

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Capability{Wire: wireOK}); err == nil {
		t.Error("nom vide accepté")
	}
	if err := r.Register(Capability{Name: "x"}); err == nil || !strings.Contains(err.Error(), "sans Wire") {
		t.Errorf("Wire absent accepté: %v", err)
	}
	if err := r.Register(Capability{Name: "x", Wire: wireOK}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Capability{Name: "x", Wire: wireOK}); err == nil || !strings.Contains(err.Error(), "deux fois") {
		t.Errorf("doublon accepté: %v", err)
	}
}

func TestRegistry_ListIsSortedByName(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"reconcile", "folders", "mailbox"} {
		if err := r.Register(Capability{Name: n, Wire: wireOK}); err != nil {
			t.Fatal(err)
		}
	}
	l := r.List()
	if l[0].Name != "folders" || l[1].Name != "mailbox" || l[2].Name != "reconcile" {
		t.Fatalf("ordre: %v", l)
	}
}

func TestRegister_PanicsOnInvalid(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register aurait dû paniquer")
		}
	}()
	Register(Capability{})
}
