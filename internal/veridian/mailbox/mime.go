package mailbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // charsets non UTF-8 (iso-8859-1, windows-1252...)
	gomail "github.com/emersion/go-message/mail"
)

// Plafonds de lecture : un mail piégé ne doit ni saturer la mémoire ni le stockage.
const (
	// MaxRawBytes : taille maximale du message brut lu sur IMAP et conservé chiffré.
	MaxRawBytes = 25 << 20
	// maxTextBytes : plafond d'une partie texte ou HTML retenue.
	maxTextBytes = 2 << 20
	// maxAttachmentBytes : plafond de lecture d'une pièce (empreinte et taille).
	maxAttachmentBytes = 25 << 20
	// maxParts : nombre maximal de parties MIME parcourues.
	maxParts = 200
	// maxDepth : profondeur maximale d'imbrication MIME.
	maxDepth = 12
	// maxHeaderValue : plafond d'une valeur d'en-tête stockée.
	maxHeaderValue = 4096
)

// Parsed est un mail brut lu et décodé.
type Parsed struct {
	MessageID  string // avec chevrons
	InReplyTo  string // avec chevrons (première valeur)
	References []string
	From       string // adresse
	FromName   string
	To         []string
	Cc         []string
	ReplyTo    string
	Subject    string
	Date       time.Time
	Headers    map[string][]string

	Text string
	HTML string // brut, avant assainissement
	// DeliveryStatus : texte de la partie message/delivery-status (DSN), s'il y en a une.
	DeliveryStatus string
	Attachments    []Attachment
	// CIDs : contenu des images incorporées, par Content-ID, pour le rendu HTML.
	CIDs map[string]InlineImage
	// Truncated : une erreur de lecture a coupé le message (message brut tronqué ou corrompu).
	Truncated bool
}

// InlineImage est une image incorporée (cid:) assez petite pour être inlinée en data URI.
type InlineImage struct {
	ContentType string
	Data        []byte
}

// maxInlineImageBytes : au-delà, l'image reste une pièce jointe sans être inlinée.
const maxInlineImageBytes = 512 << 10

// ParseRaw lit un message RFC 822. Tolérant : un message tronqué ou mal formé rend ce qui a
// pu être lu (Truncated=true), jamais une erreur sauf message totalement illisible.
func ParseRaw(raw []byte) (*Parsed, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("message vide")
	}
	ent, err := message.Read(bytes.NewReader(raw))
	if ent == nil {
		if err == nil {
			err = errors.New("message vide")
		}
		return nil, fmt.Errorf("lecture du message: %w", err)
	}
	// message.Read rend (entité, err) avec err de type charset inconnu : l'entité reste utilisable.
	p := &Parsed{Headers: map[string][]string{}, CIDs: map[string]InlineImage{}}
	hdr := gomail.Header{Header: ent.Header}

	p.Headers = extractHeaders(ent.Header)
	if id, e := hdr.MessageID(); e == nil && id != "" {
		p.MessageID = "<" + id + ">"
	} else if v := strings.TrimSpace(ent.Header.Get("Message-Id")); v != "" {
		p.MessageID = normalizeMsgID(v)
	}
	if ids, e := hdr.MsgIDList("In-Reply-To"); e == nil && len(ids) > 0 {
		p.InReplyTo = "<" + ids[0] + ">"
	} else if v := strings.TrimSpace(ent.Header.Get("In-Reply-To")); v != "" {
		p.InReplyTo = normalizeMsgID(firstField(v))
	}
	if ids, e := hdr.MsgIDList("References"); e == nil {
		for _, id := range ids {
			p.References = append(p.References, "<"+id+">")
		}
	}
	if len(p.References) == 0 {
		for _, f := range strings.Fields(ent.Header.Get("References")) {
			if id := normalizeMsgID(f); id != "" {
				p.References = append(p.References, id)
			}
		}
	}
	if list, e := hdr.AddressList("From"); e == nil && len(list) > 0 {
		p.From = strings.ToLower(list[0].Address)
		p.FromName = list[0].Name
	} else if a, e2 := mail.ParseAddress(ent.Header.Get("From")); e2 == nil {
		p.From = strings.ToLower(a.Address)
		p.FromName = a.Name
	}
	p.To = addrList(hdr, "To")
	p.Cc = addrList(hdr, "Cc")
	if list := addrList(hdr, "Reply-To"); len(list) > 0 {
		p.ReplyTo = list[0]
	}
	if s, e := hdr.Subject(); e == nil {
		p.Subject = cleanLine(s)
	} else {
		p.Subject = cleanLine(ent.Header.Get("Subject"))
	}
	if d, e := hdr.Date(); e == nil {
		p.Date = d
	}

	parts := 0
	w := &walker{p: p, parts: &parts}
	if werr := w.walk(ent, 0); werr != nil {
		p.Truncated = true
	}
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		p.Truncated = true
	}
	return p, nil
}

type walker struct {
	p     *Parsed
	parts *int
	// attIndex : rang de la prochaine pièce jointe.
	attIndex int
}

func (w *walker) walk(e *message.Entity, depth int) error {
	*w.parts++
	if *w.parts > maxParts || depth > maxDepth {
		return errors.New("trop de parties MIME")
	}
	mt, params, _ := e.Header.ContentType()
	mt = strings.ToLower(mt)
	if mr := e.MultipartReader(); mr != nil {
		for {
			child, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if child == nil {
				return err
			}
			// err peut être un charset ou un encodage inconnu : la partie reste lisible.
			if werr := w.walk(child, depth+1); werr != nil {
				return werr
			}
		}
	}

	disp, dparams, _ := e.Header.ContentDisposition()
	disp = strings.ToLower(disp)
	name := dparams["filename"]
	if name == "" {
		name = params["name"]
	}
	cid := strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>")

	isText := mt == "text/plain" || mt == "text/html" || mt == ""
	if isText && disp != "attachment" && name == "" {
		b, err := io.ReadAll(io.LimitReader(e.Body, maxTextBytes+1))
		if len(b) > maxTextBytes {
			b = b[:maxTextBytes]
		}
		s := strings.ToValidUTF8(string(b), "�")
		if mt == "text/html" {
			if w.p.HTML == "" {
				w.p.HTML = s
			} else {
				w.p.HTML += "\n" + s
			}
		} else {
			if w.p.Text == "" {
				w.p.Text = s
			} else {
				w.p.Text += "\n" + s
			}
		}
		if err != nil {
			return err
		}
		return nil
	}

	if mt == "message/delivery-status" || mt == "message/disposition-notification" {
		b, _ := io.ReadAll(io.LimitReader(e.Body, 64<<10))
		if mt == "message/delivery-status" {
			w.p.DeliveryStatus += string(b)
		}
		return nil
	}

	// Pièce jointe (ou ressource incorporée, ou message attaché).
	limited := io.LimitReader(e.Body, maxAttachmentBytes+1)
	var buf bytes.Buffer
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(&buf, h), limited)
	data := buf.Bytes()
	truncated := n > maxAttachmentBytes
	att := Attachment{
		Index:       w.attIndex,
		Name:        cleanFilename(name, mt, w.attIndex),
		ContentType: mt,
		Size:        n,
		SHA256:      hex.EncodeToString(h.Sum(nil)),
		Inline:      disp == "inline" || (cid != "" && disp != "attachment"),
		ContentID:   cid,
		Available:   !truncated,
	}
	if att.ContentType == "" {
		att.ContentType = "application/octet-stream"
	}
	w.attIndex++
	if cid != "" && strings.HasPrefix(mt, "image/") && mt != "image/svg+xml" && n <= maxInlineImageBytes && !truncated {
		w.p.CIDs[strings.ToLower(cid)] = InlineImage{ContentType: mt, Data: append([]byte(nil), data...)}
	}
	w.p.Attachments = append(w.p.Attachments, att)
	if err != nil {
		return err
	}
	return nil
}

// ReadAttachment relit le contenu de la pièce numéro index dans un message brut.
func ReadAttachment(raw []byte, index int) (Attachment, []byte, error) {
	ent, _ := message.Read(bytes.NewReader(raw))
	if ent == nil {
		return Attachment{}, nil, errors.New("message illisible")
	}
	var found *Attachment
	var content []byte
	count := 0
	parts := 0
	var rec func(e *message.Entity, depth int) bool
	rec = func(e *message.Entity, depth int) bool {
		parts++
		if parts > maxParts || depth > maxDepth {
			return false
		}
		mt, params, _ := e.Header.ContentType()
		mt = strings.ToLower(mt)
		if mr := e.MultipartReader(); mr != nil {
			for {
				child, _ := mr.NextPart()
				if child == nil {
					return false
				}
				if rec(child, depth+1) {
					return true
				}
			}
		}
		disp, dparams, _ := e.Header.ContentDisposition()
		disp = strings.ToLower(disp)
		name := dparams["filename"]
		if name == "" {
			name = params["name"]
		}
		isText := mt == "text/plain" || mt == "text/html" || mt == ""
		if (isText && disp != "attachment" && name == "") || mt == "message/delivery-status" || mt == "message/disposition-notification" {
			return false
		}
		if count == index {
			b, _ := io.ReadAll(io.LimitReader(e.Body, maxAttachmentBytes+1))
			if len(b) > maxAttachmentBytes {
				return true
			}
			cid := strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>")
			sum := sha256.Sum256(b)
			a := Attachment{Index: index, Name: cleanFilename(name, mt, index), ContentType: mt, Size: int64(len(b)),
				SHA256: hex.EncodeToString(sum[:]), ContentID: cid, Available: true}
			if a.ContentType == "" {
				a.ContentType = "application/octet-stream"
			}
			found = &a
			content = b
			return true
		}
		count++
		return false
	}
	rec(ent, 0)
	if found == nil {
		return Attachment{}, nil, errors.New("pièce jointe introuvable")
	}
	return *found, content, nil
}

func addrList(h gomail.Header, key string) []string {
	list, err := h.AddressList(key)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		if a != nil && a.Address != "" {
			out = append(out, strings.ToLower(a.Address))
		}
	}
	return out
}

func extractHeaders(h message.Header) map[string][]string {
	out := map[string][]string{}
	dec := new(mime.WordDecoder)
	f := h.Fields()
	n := 0
	for f.Next() {
		n++
		if n > 200 {
			break
		}
		k := f.Key()
		v := f.Value()
		if d, err := dec.DecodeHeader(v); err == nil {
			v = d
		}
		v = strings.ToValidUTF8(v, "�")
		if len(v) > maxHeaderValue {
			v = v[:maxHeaderValue]
		}
		out[k] = append(out[k], v)
	}
	return out
}

var spacesRe = regexp.MustCompile(`\s+`)

func cleanLine(s string) string {
	return strings.TrimSpace(spacesRe.ReplaceAllString(strings.ToValidUTF8(s, "�"), " "))
}

func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// normalizeMsgID met un identifiant entre chevrons ; "" si invalide.
func normalizeMsgID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	if s == "" || strings.ContainsAny(s, " \t\r\n<>") {
		return ""
	}
	return "<" + s + ">"
}

var badNameRe = regexp.MustCompile(`[\x00-\x1f/\\]`)

func cleanFilename(name, mt string, idx int) string {
	name = strings.TrimSpace(badNameRe.ReplaceAllString(strings.ToValidUTF8(name, "�"), "_"))
	if len(name) > 200 {
		name = name[:200]
	}
	if name != "" {
		return name
	}
	switch mt {
	case "message/rfc822":
		return "message-joint.eml"
	case "":
		return fmt.Sprintf("piece-%d", idx+1)
	}
	ext := ""
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		ext = exts[0]
	}
	return fmt.Sprintf("piece-%d%s", idx+1, ext)
}
