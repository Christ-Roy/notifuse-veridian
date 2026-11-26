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

// SigningKey : de quoi signer pour un domaine vérifié, et quand il a été contrôlé pour la dernière fois.
type SigningKey struct {
	ID          string
	Selector    string
	Key         *rsa.PrivateKey
	LastChecked time.Time
}

// KeySource : d'où vient la clé d'un domaine vérifié (le Store en production). nil, nil : domaine non vérifié dans cet espace.
type KeySource interface {
	SigningKey(ctx context.Context, ws, domain string) (*SigningKey, error)
}

// RecheckEvery : la revérification à l'usage se fait au plus une fois par cet intervalle et par domaine.
const RecheckEvery = 24 * time.Hour

type cached struct {
	key   *SigningKey
	until time.Time
}

// Signer : signature à l'envoi, avec un cache court (une requête par espace, domaine et minute au plus, réponse négative comprise).
//
// Revérification à l'usage (aucune tâche planifiée) : quand un envoi trouve un domaine dont le dernier contrôle a plus de RecheckEvery,
// il lance Recheck EN TÂCHE DE FOND, un seul à la fois par domaine et au plus un par intervalle dans ce processus, puis vide le cache.
// L'envoi qui le déclenche n'attend pas : il part avec l'état connu. Une clé retirée ou changée fait tomber le domaine (plus de
// signature dès le cache vidé) ; une panne DNS passagère le laisse vérifié (StatusOf).
type Signer struct {
	Keys KeySource
	TTL  time.Duration
	Now  func() time.Time
	// OnError : journal d'une signature impossible (le mail part alors sans notre signature, comme avant VER-166).
	OnError func(ws, domain string, err error)
	// Recheck : relit le DNS du domaine id de l'espace ws et enregistre le verdict. nil : pas de revérification.
	Recheck func(ctx context.Context, ws, id string)
	// Every : intervalle de revérification (RecheckEvery si zéro).
	Every time.Duration

	mu        sync.Mutex
	cache     map[string]cached
	inflight  map[string]bool
	attempted map[string]time.Time
	wg        sync.WaitGroup
}

func (s *Signer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Signer) every() time.Duration {
	if s.Every > 0 {
		return s.Every
	}
	return RecheckEvery
}

// Forget vide le cache d'un domaine (après création, vérification ou suppression).
func (s *Signer) Forget(ws, domain string) {
	s.mu.Lock()
	delete(s.cache, ws+"|"+domain)
	s.mu.Unlock()
}

// Wait attend la fin des revérifications lancées (tests, arrêt).
func (s *Signer) Wait() { s.wg.Wait() }

func (s *Signer) key(ctx context.Context, ws, domain string) (*SigningKey, error) {
	k := ws + "|" + domain
	s.mu.Lock()
	c, ok := s.cache[k]
	s.mu.Unlock()
	if ok && s.now().Before(c.until) {
		return c.key, nil
	}
	key, err := s.Keys.SigningKey(ctx, ws, domain)
	if err != nil {
		return nil, err
	}
	ttl := s.TTL
	if ttl == 0 {
		ttl = time.Minute
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[k] = cached{key: key, until: s.now().Add(ttl)}
	s.mu.Unlock()
	return key, nil
}

// maybeRecheck lance la revérification si le dernier contrôle est trop vieux, sans jamais bloquer l'appelant.
func (s *Signer) maybeRecheck(ws, domain string, key *SigningKey) {
	if s.Recheck == nil || key == nil {
		return
	}
	now, every := s.now(), s.every()
	if now.Sub(key.LastChecked) < every {
		return
	}
	k := ws + "|" + domain
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight, s.attempted = map[string]bool{}, map[string]time.Time{}
	}
	if last, ok := s.attempted[k]; s.inflight[k] || (ok && now.Sub(last) < every) {
		s.mu.Unlock()
		return
	}
	s.inflight[k], s.attempted[k] = true, now
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, k)
			s.mu.Unlock()
			s.Forget(ws, domain)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.Recheck(ctx, ws, key.ID)
	}()
}

// Sign : branché sur service.SetVeridianOutgoingSigner. Rend (message signé, true) si le From est sur un domaine vérifié de l'espace ;
// (nil, false) sinon, ou si la signature est impossible (journalisée) : le mail part alors tel quel.
func (s *Signer) Sign(ctx context.Context, ws, from string, raw []byte) ([]byte, bool) {
	domain := FromDomain(from)
	if ws == "" || domain == "" {
		return nil, false
	}
	key, err := s.key(ctx, ws, domain)
	if err == nil && key == nil {
		return nil, false
	}
	var out []byte
	if err == nil {
		s.maybeRecheck(ws, domain, key)
		out, err = SignMessage(raw, domain, key.Selector, key.Key)
	}
	if err != nil {
		if s.OnError != nil {
			s.OnError(ws, domain, err)
		}
		return nil, false
	}
	return out, true
}

// Rechecker : la revérification à l'usage, branchée sur Signer.Recheck. Relit le DNS et enregistre le verdict comme le bouton Vérifier.
type Rechecker struct {
	Store DomainStore
	DNS   DNSChecker
	// OnResult : journal du résultat (statut avant, après, verdict DKIM). Optionnel.
	OnResult func(ws string, before, after *Domain, err error)
}

// Run : Signer.Recheck.
func (r *Rechecker) Run(ctx context.Context, ws, id string) {
	d, err := r.Store.Get(ctx, ws, id)
	var after *Domain
	if err == nil {
		checks := r.DNS.Verify(ctx, *d)
		after, err = r.Store.SaveCheck(ctx, ws, id, StatusOf(d.Status, checks), checks)
	}
	if r.OnResult != nil {
		r.OnResult(ws, d, after, err)
	}
}
