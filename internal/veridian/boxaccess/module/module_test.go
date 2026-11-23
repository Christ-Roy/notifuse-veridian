package module

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func TestCapabilityIsRegistered(t *testing.T) {
	for _, c := range platform.Default.List() {
		if c.Name == "boites" && c.Wire != nil && c.Start != nil && c.Stop != nil {
			return
		}
	}
	t.Fatal("la capacité boites n'est pas enregistrée")
}

func TestReadSettings(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	s, err := ReadSettings(env(nil), "relais.test")
	if err != nil || s.Enabled || s.Port != 1993 || s.ShowPort != 1993 || s.Host != "relais.test" || s.Proxy {
		t.Fatalf("par défaut rien n'écoute : %+v %v", s, err)
	}
	s, err = ReadSettings(env(map[string]string{"VERIDIAN_IMAP_ENABLED": "true", "VERIDIAN_IMAP_PORT": "2993", "VERIDIAN_IMAP_PUBLIC_PORT": "443", "VERIDIAN_BOX_HOST": "imap.test", "VERIDIAN_IMAP_BEHIND_TLS_PROXY": "true"}), "x")
	if err != nil || !s.Enabled || s.Port != 2993 || s.ShowPort != 443 || s.Host != "imap.test" || !s.Proxy {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := ReadSettings(env(map[string]string{"VERIDIAN_IMAP_PORT": "99999"}), "x"); err == nil {
		t.Fatal("port invalide accepté")
	}
}

func TestStartStop_WithoutServer(t *testing.T) {
	server = nil
	if err := start(nil, nil); err != nil { //nolint:staticcheck // aucun contexte lu sans serveur
		t.Fatal(err)
	}
	if err := stop(nil); err != nil { //nolint:staticcheck
		t.Fatal(err)
	}
}
