package domauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-msgauth/dkim"
)

// signedHeaders : en-têtes signés quand ils sont présents (RFC 6376 5.4.1). From, To et Subject sont signés une fois de plus
// (sur-signature) : un en-tête de ce nom ajouté en route casse la signature.
var signedHeaders = []string{"From", "Reply-To", "Subject", "Date", "To", "Cc", "Message-ID", "In-Reply-To", "References",
	"MIME-Version", "Content-Type", "Content-Transfer-Encoding", "List-Unsubscribe", "List-Unsubscribe-Post"}

var overSigned = []string{"From", "To", "Subject"}

// headerKeys : les en-têtes de la liste présents dans le message, plus la sur-signature.
func headerKeys(raw []byte) []string {
	end := bytes.Index(raw, []byte("\r\n\r\n"))
	if end < 0 {
		end = len(raw)
	}
	present := map[string]int{}
	for _, line := range strings.Split(string(raw[:end]), "\r\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if k, _, ok := strings.Cut(line, ":"); ok {
			present[textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))]++
		}
	}
	var keys []string
	for _, k := range signedHeaders {
		for i := 0; i < present[textproto.CanonicalMIMEHeaderKey(k)]; i++ {
			keys = append(keys, k)
		}
	}
	return append(keys, overSigned...)
}

// SignMessage signe le message (CRLF) en d=domain, s=selector, relaxed/relaxed, rsa-sha256. Rend l'en-tête DKIM-Signature suivi du message.
func SignMessage(raw []byte, domain, selector string, key crypto.Signer) ([]byte, error) {
	var out bytes.Buffer
	opts := &dkim.SignOptions{Domain: domain, Selector: selector, Signer: key, Hash: crypto.SHA256,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed, BodyCanonicalization: dkim.CanonicalizationRelaxed,
		HeaderKeys: headerKeys(raw)}
	if err := dkim.Sign(&out, bytes.NewReader(raw), opts); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// FromDomain : le domaine (minuscules) d'une adresse.
func FromDomain(addr string) string {
	if a, err := mail.ParseAddress(addr); err == nil {
		addr = a.Address
	}
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(addr[i+1:])), ".")
}

// KeySource : d'où vient la clé d'un domaine vérifié (le Store en production).
type KeySource interface {
	SigningKey(ctx context.Context, ws, domain string) (string, *rsa.PrivateKey, error)
}

type cached struct {
	selector string
	key      *rsa.PrivateKey
	until    time.Time
}

// Signer : signature à l'envoi, avec un cache court (une requête par espace, domaine et minute au plus, réponse négative comprise).
type Signer struct {
	Keys KeySource
	TTL  time.Duration
	Now  func() time.Time
	// OnError : journal d'une signature impossible (le mail part alors sans notre signature, comme avant VER-166).
	OnError func(ws, domain string, err error)

	mu    sync.Mutex
	cache map[string]cached
}

func (s *Signer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Forget vide le cache d'un domaine (après création, vérification ou suppression).
func (s *Signer) Forget(ws, domain string) {
	s.mu.Lock()
	delete(s.cache, ws+"|"+domain)
	s.mu.Unlock()
}

func (s *Signer) key(ctx context.Context, ws, domain string) (string, *rsa.PrivateKey, error) {
	k := ws + "|" + domain
	s.mu.Lock()
	c, ok := s.cache[k]
	s.mu.Unlock()
	if ok && s.now().Before(c.until) {
		return c.selector, c.key, nil
	}
	sel, key, err := s.Keys.SigningKey(ctx, ws, domain)
	if err != nil {
		return "", nil, err
	}
	ttl := s.TTL
	if ttl == 0 {
		ttl = time.Minute
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[k] = cached{selector: sel, key: key, until: s.now().Add(ttl)}
	s.mu.Unlock()
	return sel, key, nil
}

// Sign : branché sur service.SetVeridianOutgoingSigner. Rend (message signé, true) si le From est sur un domaine vérifié de l'espace ;
// (nil, false) sinon, ou si la signature est impossible (journalisée) : le mail part alors tel quel.
func (s *Signer) Sign(ctx context.Context, ws, from string, raw []byte) ([]byte, bool) {
	domain := FromDomain(from)
	if ws == "" || domain == "" {
		return nil, false
	}
	sel, key, err := s.key(ctx, ws, domain)
	if err == nil && key == nil {
		return nil, false
	}
	var out []byte
	if err == nil {
		out, err = SignMessage(raw, domain, sel, key)
	}
	if err != nil {
		if s.OnError != nil {
			s.OnError(ws, domain, err)
		}
		return nil, false
	}
	return out, true
}
