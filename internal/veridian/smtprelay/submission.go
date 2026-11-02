package smtprelay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"unicode/utf8"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
)

// maxMIMEDepth borne l'imbrication multipart (anti-bombe).
const maxMIMEDepth = 5

// Submission est un message MIME recompose en champs d'envoi (phase 1 : pas
// d'octet pour octet, cf. fiche 71).
type Submission struct {
	HeaderFrom         string
	FromName           string
	Subject            string
	Text               string
	HTML               string
	MessageID          string // sans chevrons
	ReplyTo            string
	InReplyTo          string   // avec chevrons
	References         []string // avec chevrons
	ListUnsubscribeURL string
	Attachments        []domain.Attachment
}

// IsRelayBody dit si le message est un relais et non un message legacy JSON. Meme
// regle que extractJSONPayload : un corps (ou une partie text/plain ou application/json
// en multipart) qui est du JSON valide reste sur le chemin legacy. Tout autre
// message MIME lisible est un relais. Un message illisible reste legacy (son erreur
// d'origine est conservee).
func IsRelayBody(data []byte) bool {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return false
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return false
	}
	ct := msg.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		mediaType, params, err := mime.ParseMediaType(ct)
		if err == nil && strings.HasPrefix(mediaType, "multipart/") {
			mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				pct := part.Header.Get("Content-Type")
				if strings.HasPrefix(pct, "text/plain") || strings.HasPrefix(pct, "application/json") {
					raw, err := io.ReadAll(part)
					if err == nil && json.Valid(raw) {
						return false
					}
				}
			}
		}
	}
	return !json.Valid(body)
}

// ParseSubmission lit le MIME et rend les champs d'envoi. Une erreur est un
// *smtp.SMTPError 550 5.6.0 (message malforme ou non envoyable).
func ParseSubmission(data []byte) (*Submission, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, smtpError(550, 5, 6, 0, "message malforme")
	}
	from, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		return nil, smtpError(550, 5, 6, 0, "en-tete From absent ou invalide")
	}

	sub := &Submission{
		HeaderFrom: from.Address,
		FromName:   from.Name,
		MessageID:  strings.Trim(strings.TrimSpace(msg.Header.Get("Message-ID")), "<> "),
	}
	dec := new(mime.WordDecoder)
	subject := msg.Header.Get("Subject")
	if decoded, err := dec.DecodeHeader(subject); err == nil {
		subject = decoded
	}
	sub.Subject = subject

	if rt := msg.Header.Get("Reply-To"); rt != "" {
		if addrs, err := mail.ParseAddressList(rt); err == nil && len(addrs) > 0 {
			sub.ReplyTo = addrs[0].Address
		}
	}
	if irt := strings.TrimSpace(msg.Header.Get("In-Reply-To")); irt != "" {
		sub.InReplyTo = bracket(irt)
	}
	for _, ref := range strings.Fields(msg.Header.Get("References")) {
		sub.References = append(sub.References, bracket(ref))
	}
	sub.ListUnsubscribeURL = listUnsubscribeURL(msg.Header.Get("List-Unsubscribe"))

	acc := &bodyAcc{}
	if err := walkPart(textproto.MIMEHeader(msg.Header), msg.Body, 0, acc); err != nil {
		return nil, smtpError(550, 5, 6, 0, "corps MIME non lisible: "+err.Error())
	}
	sub.Text = acc.text
	sub.HTML = acc.html
	sub.Attachments = acc.attachments
	if sub.Text == "" && sub.HTML == "" && len(sub.Attachments) == 0 {
		return nil, smtpError(550, 5, 6, 0, "corps vide")
	}
	return sub, nil
}

// bodyAcc accumule les parties lues.
type bodyAcc struct {
	text        string
	html        string
	attachments []domain.Attachment
}

// walkPart parcourt une partie MIME et range texte, HTML et pieces jointes.
func walkPart(header textproto.MIMEHeader, body io.Reader, depth int, acc *bodyAcc) error {
	if depth > maxMIMEDepth {
		return fmt.Errorf("MIME trop imbrique")
	}
	ct := header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("Content-Type invalide")
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("multipart sans frontiere")
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := walkPart(part.Header, part, depth+1, acc); err != nil {
				return err
			}
		}
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	payload, err := decodeTransfer(header.Get("Content-Transfer-Encoding"), raw)
	if err != nil {
		return err
	}

	disp, dparams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dparams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	isAttachment := disp == "attachment" || (filename != "" && disp != "inline")

	if !isAttachment && mediaType == "text/plain" {
		text, err := toUTF8(payload, params["charset"])
		if err != nil {
			return err
		}
		acc.text = joinParts(acc.text, text)
		return nil
	}
	if !isAttachment && mediaType == "text/html" {
		html, err := toUTF8(payload, params["charset"])
		if err != nil {
			return err
		}
		acc.html = joinParts(acc.html, html)
		return nil
	}

	if filename == "" {
		filename = "attachment"
	}
	dispo := "attachment"
	if disp == "inline" {
		dispo = "inline"
	}
	acc.attachments = append(acc.attachments, domain.Attachment{
		Filename:    filename,
		Content:     base64.StdEncoding.EncodeToString(payload),
		ContentType: mediaType,
		Disposition: dispo,
	})
	return nil
}

// decodeTransfer annule le Content-Transfer-Encoding (base64, quoted-printable, sinon brut).
func decodeTransfer(cte string, raw []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "base64":
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(raw))
		out, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("base64 invalide")
		}
		return out, nil
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
	default:
		return raw, nil
	}
}

// toUTF8 convertit un corps dans un charset gere : UTF-8, US-ASCII, ISO-8859-1 et
// Windows-1252 (traites comme ISO-8859-1 : seuls les octets 0x80-0x9F different).
// Tout autre charset est refuse plutot que mal affiche.
func toUTF8(b []byte, charset string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return string(b), nil
	case "iso-8859-1", "latin1", "windows-1252", "cp1252":
		runes := make([]rune, 0, len(b))
		for _, c := range b {
			runes = append(runes, rune(c))
		}
		return string(runes), nil
	default:
		if utf8.Valid(b) {
			return string(b), nil
		}
		return "", fmt.Errorf("charset %q non supporte", charset)
	}
}

func joinParts(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}

// bracket garantit les chevrons autour d'un Message-ID.
func bracket(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") {
		return id
	}
	return "<" + strings.Trim(id, "<>") + ">"
}

// listUnsubscribeURL extrait la premiere URL http(s) de l'en-tete List-Unsubscribe.
func listUnsubscribeURL(h string) string {
	for {
		start := strings.Index(h, "<")
		if start < 0 {
			return ""
		}
		end := strings.Index(h[start:], ">")
		if end < 0 {
			return ""
		}
		candidate := h[start+1 : start+end]
		if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
			return candidate
		}
		h = h[start+end+1:]
	}
}

// domainOf rend la partie domaine d'une adresse (journaux sans adresse complete).
func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return ""
}

// textAsHTML rend un texte brut lisible en HTML (corps de remplacement).
func textAsHTML(text string) string {
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(text)
	return `<pre style="font-family:inherit;white-space:pre-wrap">` + escaped + `</pre>`
}

// CheckSize refuse un message au-dela de MaxMessageBytes (552 5.3.4).
func CheckSize(n int) error {
	if n > MaxMessageBytes {
		return smtpError(552, 5, 3, 4, fmt.Sprintf("message trop gros (maximum %d octets)", MaxMessageBytes))
	}
	return nil
}

// CheckRecipients refuse plus de MaxRecipients destinataires (552 5.5.3).
func CheckRecipients(n int) error {
	if n > MaxRecipients {
		return smtpError(552, 5, 5, 3, fmt.Sprintf("trop de destinataires (maximum %d)", MaxRecipients))
	}
	return nil
}

// SingleWorkspace rend l'unique workspace d'une cle. Zero ou plusieurs : 550 5.7.1
// (une cle ne doit jamais choisir un workspace au hasard).
func SingleWorkspace(ids []string) (string, error) {
	seen := map[string]bool{}
	var unique []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	switch len(unique) {
	case 1:
		return unique[0], nil
	case 0:
		return "", smtpError(550, 5, 7, 1, "cle sans workspace")
	default:
		return "", smtpError(550, 5, 7, 1, "cle rattachee a plusieurs workspaces, utilisez une cle scopee")
	}
}

// CheckFrom exige que l'en-tete From et, s'il est renseigne, l'enveloppe MAIL FROM
// soient des expediteurs du profil reserve (casse ignoree). Sinon 550 5.7.1.
func CheckFrom(headerFrom, envelopeFrom string, senders []domain.EmailSender) error {
	allowed := make(map[string]bool, len(senders))
	for _, s := range senders {
		allowed[strings.ToLower(strings.TrimSpace(s.Email))] = true
	}
	if !allowed[strings.ToLower(strings.TrimSpace(headerFrom))] {
		return smtpError(550, 5, 7, 1, "expediteur non autorise pour ce workspace")
	}
	env := strings.Trim(strings.TrimSpace(envelopeFrom), "<>")
	if env != "" && !allowed[strings.ToLower(env)] {
		return smtpError(550, 5, 7, 1, "expediteur d'enveloppe non autorise pour ce workspace")
	}
	return nil
}

// ValidateRelayConfig refuse le relais sans TLS : l'AUTH partirait en clair.
func ValidateRelayConfig(enabled bool, tlsMode string) error {
	if enabled && tlsMode == smtp_bridge.ModeOff {
		return fmt.Errorf("relais SMTP refuse : SMTP_BRIDGE_TLS=off (l'AUTH serait en clair)")
	}
	return nil
}
