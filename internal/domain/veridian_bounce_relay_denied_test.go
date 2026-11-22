package domain

import "testing"

// VER-220 (23/11/2026) : « Relay access denied » renvoyé par le serveur du destinataire veut dire
// que ce serveur n'héberge pas le domaine (MX périmé, domaine parti ailleurs) : l'adresse est
// injoignable, c'est une adresse morte. Diagnostics réels relevés en prod (robertbrunon, octobre).
func TestClassifyBounce_RelayAccessDeniedIsHard(t *testing.T) {
	for _, diag := range []string{
		"smtp; 554 5.7.1 <pax@merlintour.com>: Relay access denied",
		"smtp; 554 5.7.1 <contact@3aqualite.com>: Relay access denied",
		"554 5.7.1 <x@y.fr>: Relay access denied (in reply to RCPT TO command)",
	} {
		got := ClassifyBounce(BounceInput{Provider: EmailProviderKindSMTP, Type: "Bounce", Subtype: "5.7.1", Diagnostic: diag})
		if got != BounceClassificationHard {
			t.Fatalf("%q : attendu Hard, obtenu %v", diag, got)
		}
	}
	// Un vrai refus de politique (filtre, liste noire) reste un refus de politique.
	for _, diag := range []string{
		"smtp; 554 5.7.1 Spam message rejected",
		"smtp; 554 5.7.1 Client host 37.187.199.185 blocked using ZEN",
		"smtp; 554 5.7.1 Service unavailable; Client host [51.210.7.44] blocked",
	} {
		got := ClassifyBounce(BounceInput{Provider: EmailProviderKindSMTP, Type: "Bounce", Subtype: "5.7.1", Diagnostic: diag})
		if got != BounceClassificationPolicyRefusal {
			t.Fatalf("%q : attendu PolicyRefusal, obtenu %v", diag, got)
		}
	}
	if veridianIsRelayAccessDenied("4.7.1", "451 4.7.1 Relay access denied, try later") {
		t.Fatal("un refus temporaire (4.x) n'est pas une adresse morte")
	}
	if veridianIsRelayAccessDenied("5.4.1", "smtp; 550 5.4.1 Relay access denied") {
		t.Fatal("un 5.4.1 garde la règle d'octobre (TestClassifyBounce)")
	}
}
