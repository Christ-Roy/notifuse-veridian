package smtprelay

import (
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
)

const multipartMail = "From: Ana <ana@client.example>\r\n" +
	"To: bob@example.org\r\n" +
	"Subject: =?UTF-8?Q?Caf=C3=A9?=\r\n" +
	"Message-ID: <m1@client.example>\r\n" +
	"Reply-To: support@client.example\r\n" +
	"In-Reply-To: parent@client.example\r\n" +
	"References: <r1@client.example> <r2@client.example>\r\n" +
	"List-Unsubscribe: <mailto:unsub@client.example>, <https://client.example/unsub?x=1>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"MIX\"\r\n\r\n" +
	"--MIX\r\n" +
	"Content-Type: multipart/alternative; boundary=\"ALT\"\r\n\r\n" +
	"--ALT\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"Bonjour =C3=A9t=C3=A9\r\n" +
	"--ALT\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n\r\n" +
	"<p>Bonjour</p>\r\n" +
	"--ALT--\r\n" +
	"--MIX\r\n" +
	"Content-Type: application/pdf; name=\"facture.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"facture.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n\r\n" +
	"JVBERi0=\r\n" +
	"--MIX--\r\n"

func TestIsRelayBody(t *testing.T) {
	jsonBody := "From: a@client.example\r\nContent-Type: text/plain\r\n\r\n{\"workspace_id\":\"w\",\"notification\":{}}\r\n"
	plain := "From: a@client.example\r\nContent-Type: text/plain\r\n\r\nBonjour\r\n"
	multipartJSON := "From: a@client.example\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\n{\"workspace_id\":\"w\"}\r\n--B--\r\n"
	multipartNoJSON := "From: a@client.example\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n--B\r\nContent-Type: text/plain\r\n\r\nBonjour\r\n--B--\r\n"

	cases := []struct {
		name string
		data string
		want bool
	}{
		{"corps JSON legacy", jsonBody, false},
		{"partie JSON legacy en multipart", multipartJSON, false},
		{"texte brut : relais", plain, true},
		{"multipart sans JSON : relais", multipartNoJSON, true},
		{"illisible : reste legacy", "pas un message", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRelayBody([]byte(c.data)); got != c.want {
				t.Fatalf("IsRelayBody = %v, attendu %v", got, c.want)
			}
		})
	}
}

func TestParseSubmission(t *testing.T) {
	sub, err := ParseSubmission([]byte(multipartMail))
	if err != nil {
		t.Fatalf("ParseSubmission: %v", err)
	}
	if sub.HeaderFrom != "ana@client.example" || sub.FromName != "Ana" {
		t.Fatalf("From mal lu: %q / %q", sub.HeaderFrom, sub.FromName)
	}
	if sub.Subject != "Café" {
		t.Fatalf("Subject RFC2047 mal decode: %q", sub.Subject)
	}
	if sub.MessageID != "m1@client.example" {
		t.Fatalf("Message-ID sans chevrons attendu, got %q", sub.MessageID)
	}
	if sub.Text != "Bonjour été" {
		t.Fatalf("texte quoted-printable mal decode: %q", sub.Text)
	}
	if sub.HTML != "<p>Bonjour</p>" {
		t.Fatalf("HTML mal lu: %q", sub.HTML)
	}
	if sub.ReplyTo != "support@client.example" || sub.InReplyTo != "<parent@client.example>" {
		t.Fatalf("Reply-To / In-Reply-To faux: %q / %q", sub.ReplyTo, sub.InReplyTo)
	}
	if len(sub.References) != 2 || sub.References[0] != "<r1@client.example>" {
		t.Fatalf("References faux: %v", sub.References)
	}
	if sub.ListUnsubscribeURL != "https://client.example/unsub?x=1" {
		t.Fatalf("List-Unsubscribe doit garder l'URL https: %q", sub.ListUnsubscribeURL)
	}
	if len(sub.Attachments) != 1 {
		t.Fatalf("une piece jointe attendue, got %d", len(sub.Attachments))
	}
	att := sub.Attachments[0]
	if att.Filename != "facture.pdf" || att.ContentType != "application/pdf" || att.Content != "JVBERi0=" || att.Disposition != "attachment" {
		t.Fatalf("piece jointe fausse: %+v", att)
	}
}

func TestParseSubmission_ISO88591(t *testing.T) {
	data := "From: a@client.example\r\nSubject: x\r\nContent-Type: text/plain; charset=iso-8859-1\r\n\r\nCaf\xe9\r\n"
	sub, err := ParseSubmission([]byte(data))
	if err != nil {
		t.Fatalf("ParseSubmission ISO-8859-1: %v", err)
	}
	if strings.TrimRight(sub.Text, "\r\n") != "Café" {
		t.Fatalf("ISO-8859-1 doit etre converti en UTF-8, got %q", sub.Text)
	}
}

func TestParseSubmission_Malformed(t *testing.T) {
	_, err := ParseSubmission([]byte("Subject: sans expediteur\r\n\r\nx\r\n"))
	if se := asSMTP(t, err); se.Code != 550 {
		t.Fatalf("sans From doit rendre 550, got %d", se.Code)
	}
}

func TestParseSubmission_EmptyBody(t *testing.T) {
	_, err := ParseSubmission([]byte("From: a@client.example\r\nContent-Type: text/plain\r\n\r\n"))
	if se := asSMTP(t, err); se.Code != 550 || !strings.Contains(se.Message, "corps vide") {
		t.Fatalf("corps vide doit rendre 550 corps vide, got %+v", se)
	}
}

func TestCheckSize(t *testing.T) {
	if err := CheckSize(MaxMessageBytes); err != nil {
		t.Fatalf("la taille limite doit passer: %v", err)
	}
	if se := asSMTP(t, CheckSize(MaxMessageBytes+1)); se.Code != 552 {
		t.Fatalf("au-dela de la limite: 552 attendu, got %d", se.Code)
	}
}

func TestCheckRecipients(t *testing.T) {
	if err := CheckRecipients(MaxRecipients); err != nil {
		t.Fatalf("le maximum de destinataires doit passer: %v", err)
	}
	if se := asSMTP(t, CheckRecipients(MaxRecipients+1)); se.Code != 552 {
		t.Fatalf("au-dela du maximum: 552 attendu, got %d", se.Code)
	}
}

func TestSingleWorkspace(t *testing.T) {
	if id, err := SingleWorkspace([]string{"ws1", "ws1"}); err != nil || id != "ws1" {
		t.Fatalf("doublon du meme workspace doit donner ws1: %q %v", id, err)
	}
	if _, err := SingleWorkspace(nil); asSMTP(t, err).Code != 550 {
		t.Fatal("aucun workspace doit rendre 550")
	}
	if _, err := SingleWorkspace([]string{"ws1", "ws2"}); asSMTP(t, err).Code != 550 {
		t.Fatal("deux workspaces doivent rendre 550")
	}
}

func TestCheckFrom(t *testing.T) {
	senders := []domain.EmailSender{{Email: "noreply@client.example"}}
	if err := CheckFrom("NoReply@Client.example", "noreply@client.example", senders); err != nil {
		t.Fatalf("expediteur autorise (casse ignoree) refuse: %v", err)
	}
	if err := CheckFrom("noreply@client.example", "<>", senders); err != nil {
		t.Fatalf("enveloppe vide (bounce) doit passer: %v", err)
	}
	if err := CheckFrom("ceo@autre.example", "", senders); asSMTP(t, err).Code != 550 {
		t.Fatal("From non autorise doit rendre 550")
	}
	if err := CheckFrom("noreply@client.example", "evil@autre.example", senders); asSMTP(t, err).Code != 550 {
		t.Fatal("enveloppe non autorisee doit rendre 550")
	}
}

func TestValidateRelayConfig(t *testing.T) {
	if err := ValidateRelayConfig(true, smtp_bridge.ModeOff); err == nil {
		t.Fatal("relais actif sans TLS doit etre refuse")
	}
	if err := ValidateRelayConfig(true, smtp_bridge.ModeSTARTTLS); err != nil {
		t.Fatalf("relais avec starttls doit passer: %v", err)
	}
	if err := ValidateRelayConfig(false, smtp_bridge.ModeOff); err != nil {
		t.Fatalf("relais desactive: aucune contrainte: %v", err)
	}
}
