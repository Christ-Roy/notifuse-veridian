package domain

import "testing"

func TestValidateVeridianEHLOHostname(t *testing.T) {
	for _, ok := range []string{"", "mail.exemple.fr", "smtp-1.agences-veridian.fr", "localhost", "[192.0.2.1]", "[2001:db8::1]"} {
		e := &EmailProvider{SMTP: &SMTPSettings{EHLOHostname: ok}}
		if err := e.ValidateVeridianEHLOHostname(); err != nil {
			t.Errorf("%q refusé : %v", ok, err)
		}
	}
	for _, ko := range []string{"a\r\nRSET", "x\nMAIL FROM:<a@b.c>", "mail exemple.fr", "-mail.fr", "mail.fr-", "a;b", "[1.2.3.4", "é.fr"} {
		e := &EmailProvider{SMTP: &SMTPSettings{EHLOHostname: ko}}
		if err := e.ValidateVeridianEHLOHostname(); err == nil {
			t.Errorf("%q accepté", ko)
		}
	}
	if err := (&EmailProvider{}).ValidateVeridianEHLOHostname(); err != nil {
		t.Errorf("sans SMTP : %v", err)
	}
}

func TestValidateVeridianSettings_EnchaineLesControles(t *testing.T) {
	if err := (&EmailProvider{SMTP: &SMTPSettings{EHLOHostname: "a\r\nRSET"}}).ValidateVeridianSettings(); err == nil {
		t.Fatal("le contrôle EHLO n'est pas enchaîné")
	}
	if err := (&EmailProvider{SMTP: &SMTPSettings{EHLOHostname: "mail.exemple.fr"}}).ValidateVeridianSettings(); err != nil {
		t.Fatalf("profil sain refusé : %v", err)
	}
}
