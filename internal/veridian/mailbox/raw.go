package mailbox

import (
	"bytes"
	"strings"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
)

// Reconstruct fabrique un message RFC 822 à partir du rendu conservé d'un mail ENVOYÉ (Notifuse
// ne garde pas les octets partis). L'en-tête X-Veridian-Reconstructed le dit : même contenu, pas
// la copie octet pour octet.
func Reconstruct(m *Message) []byte {
	var hdr gomail.Header
	hdr.SetDate(m.OccurredAt)
	hdr.SetSubject(m.Subject)
	if m.From != "" {
		hdr.SetAddressList("From", []*gomail.Address{{Name: m.FromName, Address: m.From}})
	}
	if len(m.To) > 0 {
		hdr.SetAddressList("To", toAddrs(m.To))
	}
	if len(m.Cc) > 0 {
		hdr.SetAddressList("Cc", toAddrs(m.Cc))
	}
	if id := strings.Trim(m.MessageID, "<>"); id != "" {
		hdr.SetMessageID(id)
	}
	if m.InReplyTo != "" {
		hdr.Set("In-Reply-To", m.InReplyTo)
	}
	if len(m.References) > 0 {
		hdr.Set("References", strings.Join(m.References, " "))
	}
	hdr.Set("X-Veridian-Reconstructed", "oui")
	hdr.Set("X-Veridian-Body-Source", m.BodySource)
	hdr.Set("MIME-Version", "1.0")

	text, htm := m.BodyText, m.BodyHTMLSanitized
	if text == "" && htm == "" {
		text = "(contenu non conservé)"
	}
	var buf bytes.Buffer
	if htm == "" {
		hdr.Set("Content-Type", "text/plain; charset=utf-8")
		w, err := message.CreateWriter(&buf, hdr.Header)
		if err != nil {
			return nil
		}
		_, _ = w.Write([]byte(text))
		_ = w.Close()
		return buf.Bytes()
	}
	hdr.Set("Content-Type", "multipart/alternative")
	w, err := message.CreateWriter(&buf, hdr.Header)
	if err != nil {
		return nil
	}
	part := func(ct, body string) {
		var ph message.Header
		ph.Set("Content-Type", ct+"; charset=utf-8")
		ph.Set("Content-Transfer-Encoding", "quoted-printable")
		pw, e := w.CreatePart(ph)
		if e != nil {
			return
		}
		_, _ = pw.Write([]byte(body))
		_ = pw.Close()
	}
	if text != "" {
		part("text/plain", text)
	}
	part("text/html", htm)
	_ = w.Close()
	return buf.Bytes()
}

func toAddrs(list []string) []*gomail.Address {
	out := make([]*gomail.Address, 0, len(list))
	for _, a := range list {
		out = append(out, &gomail.Address{Address: a})
	}
	return out
}
