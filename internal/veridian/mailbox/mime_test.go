package mailbox

import (
	"strings"
	"testing"
	"time"
)

func TestParseRaw_ReplyAlternative(t *testing.T) {
	p, err := ParseRaw(replyHuman("<abc@x.fr>", "<root@x.fr> <abc@x.fr>"))
	if err != nil {
		t.Fatal(err)
	}
	if p.From != "marie@exemple-animaux.fr" || p.FromName != "Marie Dupont" {
		t.Fatalf("expéditeur: %q %q", p.From, p.FromName)
	}
	if p.Subject != "Re: Présentation ASD" {
		t.Fatalf("objet: %q", p.Subject)
	}
	if !strings.Contains(p.Text, "m'intéresse") {
		t.Fatalf("texte quoted-printable mal décodé: %q", p.Text)
	}
	if !strings.Contains(p.HTML, "<b>oui</b>") {
		t.Fatalf("html: %q", p.HTML)
	}
	if p.MessageID != "<rep1@exemple-animaux.fr>" || p.InReplyTo != "<abc@x.fr>" {
		t.Fatalf("ids: %q %q", p.MessageID, p.InReplyTo)
	}
	if len(p.References) != 2 || p.References[0] != "<root@x.fr>" {
		t.Fatalf("références: %v", p.References)
	}
}

func TestParseRaw_AttachmentsAndInlineCID(t *testing.T) {
	raw := withAttachment("<abc@x.fr>")
	p, err := ParseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Attachments) != 2 {
		t.Fatalf("attendu 2 pièces (logo inline + pdf), obtenu %d: %+v", len(p.Attachments), p.Attachments)
	}
	var pdf *Attachment
	for i := range p.Attachments {
		if p.Attachments[i].Name == "catalogue.pdf" {
			pdf = &p.Attachments[i]
		}
	}
	if pdf == nil || pdf.ContentType != "application/pdf" || pdf.Inline || pdf.SHA256 == "" || pdf.Size != int64(len("%PDF-1.4 fake pdf content")) {
		t.Fatalf("pdf mal décrit: %+v", pdf)
	}
	if _, ok := p.CIDs["logo@cabinet"]; !ok {
		t.Fatalf("image incorporée non retenue: %v", p.CIDs)
	}
	// relecture à la demande
	att, data, err := ReadAttachment(raw, pdf.Index)
	if err != nil || att.Name != "catalogue.pdf" || string(data) != "%PDF-1.4 fake pdf content" {
		t.Fatalf("relecture: %v %+v %q", err, att, data)
	}
	if _, _, err := ReadAttachment(raw, 9); err == nil {
		t.Fatal("une pièce absente doit échouer")
	}
}

func TestParseRaw_Latin1(t *testing.T) {
	p, err := ParseRaw(latin1Mail())
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "Café" || !strings.Contains(p.Text, "Café crème") {
		t.Fatalf("charset ISO-8859-1 mal décodé: %q / %q", p.Subject, p.Text)
	}
}

func TestParseRaw_TruncatedKeepsWhatWasRead(t *testing.T) {
	raw := withAttachment("<abc@x.fr>")
	cut := raw[:len(raw)-120]
	p, err := ParseRaw(cut)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject == "" || p.HTML == "" {
		t.Fatalf("un message tronqué doit rendre ce qui a été lu: %+v", p)
	}
}

func TestParseRaw_Garbage(t *testing.T) {
	if _, err := ParseRaw([]byte("")); err == nil {
		t.Fatal("un message vide doit échouer")
	}
	p, err := ParseRaw([]byte("pas un mail du tout"))
	if err == nil && p == nil {
		t.Fatal("ni message ni erreur")
	}
}

func TestReconstruct_RoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	m := &Message{Direction: DirectionOut, Kind: KindSent, MessageID: "<abc@d.fr>", Subject: "Présentation ASD", From: "hugo@d.fr", FromName: "Hugo",
		To: []string{"marie@x.fr"}, OccurredAt: now, BodyText: "Bonjour Marie, café ?", BodyHTMLSanitized: "<p>Bonjour <b>Marie</b></p>",
		BodySource: BodyCaptured, InReplyTo: "<j0@d.fr>", References: []string{"<j0@d.fr>"}}
	raw := Reconstruct(m)
	p, err := ParseRaw(raw)
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if p.Subject != "Présentation ASD" || p.From != "hugo@d.fr" || p.To[0] != "marie@x.fr" || p.MessageID != "<abc@d.fr>" || p.InReplyTo != "<j0@d.fr>" {
		t.Fatalf("%+v", p)
	}
	if !strings.Contains(p.Text, "café") || !strings.Contains(p.HTML, "<b>Marie</b>") {
		t.Fatalf("corps: %q %q", p.Text, p.HTML)
	}
	if p.Headers["X-Veridian-Reconstructed"] == nil {
		t.Fatal("le message reconstitué doit se déclarer tel")
	}
	m.BodyHTMLSanitized = ""
	p2, err := ParseRaw(Reconstruct(m))
	if err != nil || !strings.Contains(p2.Text, "café") || p2.HTML != "" {
		t.Fatalf("texte seul: %v %+v", err, p2)
	}
}

func TestNewThread_Milestones(t *testing.T) {
	t0 := time.Now()
	th := NewThread("t1", []Message{
		{ID: "a", Kind: KindSent, Direction: DirectionOut, Subject: "S", OccurredAt: t0, ContactEmail: "m@x.fr"},
		{ID: "b", Kind: KindSent, Direction: DirectionOut, Subject: "Re: S", OccurredAt: t0.Add(time.Hour)},
		{ID: "c", Kind: KindReplyHuman, Direction: DirectionIn, OccurredAt: t0.Add(2 * time.Hour)},
		{ID: "d", Kind: KindBounceHard, Direction: DirectionIn, OccurredAt: t0.Add(3 * time.Hour)},
	})
	got := []string{}
	for _, m := range th.Milestones {
		got = append(got, m.Label)
	}
	want := "Premier mail,Relance 1,Réponse,Rebond définitif"
	if strings.Join(got, ",") != want || th.Count != 4 || th.LastKind != KindBounceHard || th.Subject != "S" {
		t.Fatalf("%v %+v", got, th)
	}
}
