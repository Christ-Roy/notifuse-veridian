package mailbox

import (
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/veridian_ndr"
)

func TestDecodeDSN_Table(t *testing.T) {
	cases := []struct {
		name       string
		ndr        veridian_ndr.Result
		ds         string
		wantCat    string
		wantInText string
		provider   string
	}{
		{"adresse inexistante gmail", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.1.1", DiagnosticCode: "smtp; 550-5.1.1 The email account that you tried to reach does not exist", Recipient: "a@gmail.com"}, "Action: failed\nRemote-MTA: dns; gmail-smtp-in.l.google.com", CatAdresseInexistante, "Adresse inexistante", "Gmail"},
		{"boite pleine souple", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeveritySoft, DSNCode: "4.2.2", DiagnosticCode: "smtp; 452 4.2.2 Over quota"}, "Remote-MTA: dns; smtp-in.orange.fr", CatBoitePleine, "pleine pour le moment", "Orange"},
		{"refus politique", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.7.1", DiagnosticCode: "smtp; 550 5.7.1 Message rejected as spam by Content Filtering"}, "", CatRefusPolitique, "Refus de politique", ""},
		{"spf", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.7.26", DiagnosticCode: "smtp; 550 5.7.26 This message fails SPF authentication"}, "", CatAuthentification, "authentification", ""},
		{"greylisting", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeveritySoft, DSNCode: "4.7.1", DiagnosticCode: "smtp; 451 4.7.1 Greylisting in action, please come back later"}, "", CatGreylisting, "Greylisting", ""},
		{"domaine", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.1.2", DiagnosticCode: "smtp; 550 5.1.2 Domain not found"}, "", CatDomaineInexistant, "domaine", ""},
		{"trop gros", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.2.3", DiagnosticCode: "smtp; 552 message size exceeds fixed limit"}, "", CatMessageTropGros, "volumineux", ""},
		{"limite de debit", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeveritySoft, DSNCode: "4.7.0", DiagnosticCode: "smtp; 421 4.7.0 Too many messages from your IP, rate limit exceeded"}, "", CatLimiteDebit, "limite le débit", ""},
		{"adresse inexistante code vague", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.0.0", DiagnosticCode: "smtp; 550 User unknown in virtual mailbox table"}, "", CatAdresseInexistante, "Adresse inexistante", ""},
		{"livre", veridian_ndr.Result{IsNDR: true, DSNCode: "2.0.0"}, "Action: delivered\n", CatLivre, "Accusé de livraison", ""},
		{"inconnu dur", veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityHard, DSNCode: "5.0.0", DiagnosticCode: "smtp; 554 non"}, "", CatInconnu, "raison non reconnue", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DecodeDSN(c.ndr, c.ds, "")
			if d.Category != c.wantCat {
				t.Errorf("catégorie %q, attendu %q (raison: %s)", d.Category, c.wantCat, d.ReasonFR)
			}
			if !strings.Contains(d.ReasonFR, c.wantInText) {
				t.Errorf("raison %q ne contient pas %q", d.ReasonFR, c.wantInText)
			}
			if c.provider != "" && d.Provider != c.provider {
				t.Errorf("fournisseur %q, attendu %q", d.Provider, c.provider)
			}
			if d.ReasonFR == "" {
				t.Error("raison française vide")
			}
		})
	}
}

func TestDecodeDSN_SeverityFromCodeWhenUnknown(t *testing.T) {
	d := DecodeDSN(veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityUnknown, DiagnosticCode: "smtp; 550 mailbox unavailable"}, "", "")
	if d.Severity != "hard" || d.Category != CatAdresseInexistante {
		t.Fatalf("%+v", d)
	}
}

func TestDecodeDSN_DelayedAction(t *testing.T) {
	d := DecodeDSN(veridian_ndr.Result{IsNDR: true, Severity: veridian_ndr.SeverityUnknown}, "Action: delayed\nStatus: 4.4.1", "")
	if d.Category != CatRetard {
		t.Fatalf("%+v", d)
	}
}
