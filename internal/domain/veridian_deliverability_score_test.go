package domain

import (
	"testing"

	deliverability "github.com/Notifuse/notifuse/pkg/veridian_deliverability"
)

func TestVeridianParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want deliverability.Mode
	}{
		{"strict", deliverability.ModeStrict},
		{"lenient", deliverability.ModeLenient},
		{"default", deliverability.ModeDefault},
		{"", deliverability.ModeDefault},
		{"bogus", deliverability.ModeDefault},
		{"STRICT", deliverability.ModeDefault}, // case-sensitive : seul "strict" exact match
	}
	for _, c := range cases {
		if got := VeridianParseMode(c.in); got != c.want {
			t.Errorf("VeridianParseMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestVeridianDeliverabilityScoreRequest_ToLinterInput(t *testing.T) {
	req := &VeridianDeliverabilityScoreRequest{
		WorkspaceID:   "ws123",
		Subject:       "Bonjour Marie",
		Body:          "Salut, voici une proposition.",
		IsHTML:        true,
		FromDomain:    "agences-veridian.fr",
		ProviderClass: "google",
		Mode:          "strict",
	}
	in := req.ToLinterInput()

	if in.Subject != req.Subject {
		t.Errorf("Subject not propagated: got %q", in.Subject)
	}
	if in.Body != req.Body {
		t.Errorf("Body not propagated: got %q", in.Body)
	}
	if !in.IsHTML {
		t.Errorf("IsHTML not propagated")
	}
	if in.FromDomain != req.FromDomain {
		t.Errorf("FromDomain not propagated: got %q", in.FromDomain)
	}
	if in.ProviderClass != req.ProviderClass {
		t.Errorf("ProviderClass not propagated: got %q", in.ProviderClass)
	}
	if in.Mode != deliverability.ModeStrict {
		t.Errorf("Mode should map to ModeStrict, got %v", in.Mode)
	}
	// Le WorkspaceID n'est PAS un champ du linter (auth/scoping uniquement) : il
	// ne doit pas fuiter dans l'Input.
}

func TestVeridianDeliverabilityScoreRequest_ToLinterInput_EmptyModeDefaults(t *testing.T) {
	req := &VeridianDeliverabilityScoreRequest{
		WorkspaceID: "ws123",
		Body:        "texte",
	}
	in := req.ToLinterInput()
	if in.Mode != deliverability.ModeDefault {
		t.Errorf("empty mode should yield ModeDefault, got %v", in.Mode)
	}
	if in.IsHTML {
		t.Errorf("IsHTML should default false")
	}
}

// TestVeridianDeliverabilityScoreRequest_EndToEndScoring vérifie que la
// conversion + le scoring produisent un résultat cohérent (le DTO domain est
// bien branché sur le moteur pur).
func TestVeridianDeliverabilityScoreRequest_EndToEndScoring(t *testing.T) {
	req := &VeridianDeliverabilityScoreRequest{
		WorkspaceID: "ws123",
		Subject:     "RE: FREE $$$ ACT NOW!!!",
		Body:        "<img src=x> CLICK HERE {{ first_name }} {a|b} http://a http://b http://c http://d",
		IsHTML:      true,
		Mode:        "strict",
	}
	res := deliverability.Score(req.ToLinterInput())
	if !res.IsRisky {
		t.Errorf("spammy template should be risky, got score %v", res.Score)
	}
	if res.Mode != "strict" {
		t.Errorf("mode should be strict, got %q", res.Mode)
	}
}
