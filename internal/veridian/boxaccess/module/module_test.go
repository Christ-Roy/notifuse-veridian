package module

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/veridian/boxsend"
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
	s, err = ReadSettings(env(map[string]string{"VERIDIAN_IMAP_ENABLED": "true", "VERIDIAN_IMAP_PORT": "2993", "VERIDIAN_IMAP_PUBLIC_PORT": "443", "VERIDIAN_BOX_HOST": "imap.test", "VERIDIAN_IMAP_BEHIND_TLS_PROXY": "true", "VERIDIAN_IMAP_PROXY_PROTOCOL": "true",
		"VERIDIAN_BOX_SMTP_PORT": "5465", "VERIDIAN_BOX_SMTP_HOST": "smtp.test", "VERIDIAN_BOX_SMTP_PUBLIC_PORT": "443", "VERIDIAN_BOX_SMTP_PROXY_PROTOCOL": "true"}), "x")
	if err != nil || !s.Enabled || s.Port != 2993 || s.ShowPort != 443 || s.Host != "imap.test" || !s.Proxy || !s.PROXYv2 || s.SMTPPort != 5465 || s.SMTPHost != "smtp.test" || s.SMTPShow != 443 || !s.SMTPPROXYv2 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := ReadSettings(env(map[string]string{"VERIDIAN_IMAP_PORT": "99999"}), "x"); err == nil {
		t.Fatal("port invalide accepté")
	}
}

// Toute limite de la porte publique a une politique posée : sans elle, le limiteur laisse passer (essais par boîte, par adresse).
func TestGateLimits_AreDistinct(t *testing.T) {
	if boxsend.LimitGateIP == boxsend.LimitGateBox || boxsend.GateTriesBox >= boxsend.GateTriesIP {
		t.Fatal("les deux limites doivent se distinguer")
	}
}

func TestStartStop_WithoutServer(t *testing.T) {
	server, smtpServer = nil, nil
	if err := start(nil, nil); err != nil { //nolint:staticcheck // aucun contexte lu sans serveur
		t.Fatal(err)
	}
	if err := stop(nil); err != nil { //nolint:staticcheck
		t.Fatal(err)
	}
}
