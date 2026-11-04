package http

import "testing"

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// Un update SANS les champs relais ne change ni le relais ni le certificat géré.
func TestApplyRelayPatch_AbsentKeepsValues(t *testing.T) {
	cur := relayValues{SMTPRelayEnabled: true, CertManaged: true, ACMEEmail: "ops@example.test", ACMEDirectory: "production"}
	got := applyRelayPatch(cur, relayPatch{})
	if got != cur {
		t.Fatalf("champs absents : valeurs inchangées attendues, got %+v", got)
	}
}

// Un update avec false explicite coupe bien le relais.
func TestApplyRelayPatch_ExplicitFalseDisables(t *testing.T) {
	cur := relayValues{SMTPRelayEnabled: true, CertManaged: true}
	got := applyRelayPatch(cur, relayPatch{SMTPRelayEnabled: boolPtr(false)})
	if got.SMTPRelayEnabled {
		t.Fatal("false explicite : le relais doit être coupé")
	}
	if !got.CertManaged {
		t.Fatal("un seul champ fourni : le certificat géré doit rester inchangé")
	}
}

// Un e-mail et un annuaire explicites sont écrits.
func TestApplyRelayPatch_ExplicitStringsWritten(t *testing.T) {
	got := applyRelayPatch(relayValues{}, relayPatch{ACMEEmail: strPtr("ops@example.test"), ACMEDirectory: strPtr("test")})
	if got.ACMEEmail != "ops@example.test" || got.ACMEDirectory != "test" {
		t.Fatalf("valeurs explicites attendues, got %+v", got)
	}
}
