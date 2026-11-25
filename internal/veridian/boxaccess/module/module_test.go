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
	if s.STARTTLSPort != 0 || s.IMAPAlso != nil || s.SMTPAlso != nil {
		t.Fatalf("587 et autres ports éteints par défaut : %+v", s)
	}
	s, err = ReadSettings(env(map[string]string{"VERIDIAN_BOX_SMTP_PORT": "5465", "VERIDIAN_BOX_STARTTLS_PORT": "5587", "VERIDIAN_IMAP_ALSO": "443/ssl", "VERIDIAN_BOX_SMTP_ALSO": "443/ssl,587/starttls"}), "x")
	if err != nil || s.STARTTLSPort != 5587 || len(s.IMAPAlso) != 1 || len(s.SMTPAlso) != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []map[string]string{{"VERIDIAN_BOX_STARTTLS_PORT": "5587"}, {"VERIDIAN_IMAP_ALSO": "443"}, {"VERIDIAN_BOX_SMTP_ALSO": "587/tls"}} {
		if _, err := ReadSettings(env(bad), "x"); err == nil {
			t.Fatalf("%v accepté", bad)
		}
	}
	if _, err := ReadSettings(env(map[string]string{"VERIDIAN_IMAP_PORT": "99999"}), "x"); err == nil {
		t.Fatal("port invalide accepté")
	}
}

// Deux limites distinctes (par boîte, par adresse), chacune avec sa politique : sans politique, le limiteur refuse tout.
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

// Un nom ajouté au certificat en fait émettre un : smtp n'y entre que si la soumission 587 est demandée.
func TestCertNames(t *testing.T) {
	env := map[string]string{"VERIDIAN_MX_HOST": "mx.test", "VERIDIAN_BOX_SMTP_HOST": "smtp.test"}
	get := func(k string) string { return env[k] }
	if n := CertNames(get); len(n) != 1 || n[0] != "mx.test" {
		t.Fatal(n)
	}
	env["VERIDIAN_BOX_STARTTLS_PORT"] = "5587"
	if n := CertNames(get); len(n) != 2 || n[1] != "smtp.test" {
		t.Fatal(n)
	}
}
