package mxgate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/mxauth"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/emersion/go-msgauth/dkim"
)

type fake struct {
	mu       sync.Mutex
	got      []string
	raw      []byte
	err      error
	dup      bool
	msgs     []*domain.VeridianIMAPMessage
	listed   bool
	noBudget bool
}

func (f *fake) IngestRaw(_ context.Context, ws string, box mailbox.BoxInfo, ref mailbox.MailboxRef, raw []byte, _ time.Time, _ bool) (*mailbox.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, false, f.err
	}
	f.got, f.raw = append(f.got, ws+"/"+box.ProfileID+"/"+ref.IntegrationID), raw
	m := &mailbox.Message{ID: "m1", MessageID: "<r1@client.fr>", InReplyTo: "<e1@boite.fr>", From: "p@client.fr", To: []string{"jc@boite.fr"}}
	if f.dup {
		m.DuplicateOf = "m0"
	}
	return m, true, nil
}
func (f *fake) DispatchMessage(m *domain.VeridianIMAPMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, m)
	return true
}
func (f *fake) Listed(context.Context, string) (bool, string) { return f.listed, "x" }
func (f *fake) Allow(string, string) bool                     { return !f.noBudget }

type lister struct {
	ws  []*domain.Workspace
	err error
	n   int
}

func (l *lister) List(context.Context) ([]*domain.Workspace, error) { l.n++; return l.ws, l.err }

func ws(id string, profiles ...string) *domain.Workspace {
	w := &domain.Workspace{ID: id}
	for i, e := range profiles {
		w.Integrations = append(w.Integrations, domain.Integration{ID: fmt.Sprintf("%s-p%d", id, i), Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{Senders: []domain.EmailSender{{Email: e}}}})
	}
	return w
}

// dns : un DNS de poche (aucune requête réseau). client.fr interdit toute IP au SPF et publie p=reject.
type dns struct{ txt map[string][]string }

func (d dns) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := d.txt[strings.ToLower(name)]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (dns) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (dns) LookupIPAddr(_ context.Context, name string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (dns) LookupAddr(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}

func zone() dns {
	return dns{txt: map[string][]string{"client.fr": {"v=spf1 -all"}, "_dmarc.client.fr": {"v=DMARC1; p=reject"}}}
}

func start(t *testing.T, f *fake, l *lister) string {
	t.Helper()
	return startAuth(t, f, l, mxauth.New(zone()))
}

func startAuth(t *testing.T, f *fake, l *lister, auth Authenticator) string {
	t.Helper()
	s, err := New(Config{Hostname: "mx.test", Directory: NewProfiles(l, []string{" Boite.fr "}), Ingest: f, Dispatch: f, Blocklist: f, Limiter: f, Auth: auth, Logger: logger.NewLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Close() })
	return ln.Addr().String()
}

func send(addr, to string) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail("p@client.fr"); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, _ = w.Write([]byte("From: p@client.fr\r\nTo: " + to + "\r\nSubject: Re: x\r\nMessage-Id: <r1@client.fr>\r\nReferences: <e1@boite.fr>\r\n\r\nOui\r\n"))
	return w.Close()
}

func TestReceive_StoredThenDispatched(t *testing.T) {
	f, l := &fake{}, &lister{ws: []*domain.Workspace{ws("gbc", "JC@boite.fr"), ws("rb", "r@boite.fr", "x@ailleurs.fr")}}
	addr := start(t, f, l)
	if err := send(addr, "jc@BOITE.fr"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f.got) != "[gbc/gbc-p0/mx:gbc-p0]" || len(f.msgs) != 1 || f.msgs[0].WorkspaceID != "gbc" || f.msgs[0].MessageID != "r1@client.fr" || fmt.Sprint(f.msgs[0].References) == "[]" {
		t.Fatalf("%v %+v", f.got, f.msgs)
	}
	if !strings.HasPrefix(string(f.raw), "Return-Path: <p@client.fr>\r\nReceived: from [127.0.0.1] by mx.test") || !strings.HasSuffix(string(f.raw), "\r\nOui\r\n") {
		t.Fatalf("trace : %q", f.raw)
	}
	if err := send(addr, "postmaster@boite.fr"); err != nil || len(f.got) != 2 || !strings.HasPrefix(f.got[1], "gbc/") {
		t.Fatalf("postmaster : %v %v", err, f.got)
	}
	if l.n != 1 {
		t.Fatalf("annuaire relu %d fois en une minute", l.n)
	}
	f.dup = true
	if err := send(addr, "jc@boite.fr"); err != nil || len(f.msgs) != 2 {
		t.Fatalf("une copie ne relance pas les consommateurs : %v, %d", err, len(f.msgs))
	}
}

func TestReceive_Refusals(t *testing.T) {
	both := []*domain.Workspace{ws("a", "jc@boite.fr"), ws("b", "partage@boite.fr"), ws("c", "partage@boite.fr", "z@gmail.com")}
	for name, tc := range map[string]struct {
		to   string
		prep func(*fake, *lister)
		code string
	}{
		"pas d'open relay":            {to: "victime@gmail.com", code: "550"},
		"domaine non ouvert":          {to: "z@gmail.com", code: "550"},
		"adresse inconnue":            {to: "personne@boite.fr", code: "550"},
		"adresse de deux espaces":     {to: "partage@boite.fr", code: "451"},
		"annuaire en panne":           {to: "jc@boite.fr", prep: func(_ *fake, l *lister) { l.err = errors.New("base") }, code: "451"},
		"base en panne : à réessayer": {to: "jc@boite.fr", prep: func(f *fake, _ *lister) { f.err = errors.New("base") }, code: "451"},
		"liste noire":                 {to: "jc@boite.fr", prep: func(f *fake, _ *lister) { f.listed = true }, code: "554"},
		"débit":                       {to: "jc@boite.fr", prep: func(f *fake, _ *lister) { f.noBudget = true }, code: "451"},
	} {
		f, l := &fake{}, &lister{ws: both}
		if tc.prep != nil {
			tc.prep(f, l)
		}
		err := send(start(t, f, l), tc.to)
		if err == nil || !strings.HasPrefix(err.Error(), tc.code) || len(f.got) != 0 || strings.ContainsAny(err.Error(), "éèàêô") {
			t.Errorf("%s : %v (attendu %s), rangés %v", name, err, tc.code, f.got)
		}
	}
}

func TestNewAndListen(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("dépendances manquantes")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	s, err := New(Config{Addr: ln.Addr().String(), Hostname: "mx.test", Directory: NewProfiles(&lister{}, nil), Ingest: &fake{}, Limiter: &fake{}, Logger: logger.NewLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ListenAndServe(); err == nil {
		t.Fatal("port pris")
	}
}

func TestTrace_NoHeaderInjection(t *testing.T) {
	got := string(trace("1.2.3.4", "mx", "a@b>\r\nX-Mal: oui", time.Unix(0, 0), []byte("S: x\r\n\r\n")))
	if strings.Contains(got, "\nX-Mal") {
		t.Fatalf("%q", got)
	}
}

// keyLimiter note les clés demandées.
type keyLimiter struct{ keys []string }

func (k *keyLimiter) Allow(_, key string) bool { k.keys = append(k.keys, key); return true }

func TestMail_IPv6LimitedByPrefix(t *testing.T) {
	k := &keyLimiter{}
	s, err := New(Config{Hostname: "mx.test", Directory: NewProfiles(&lister{}, []string{"boite.fr"}), Ingest: &fake{}, Limiter: k, Logger: logger.NewLogger()})
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"2a02:c207:1:2::1", "2a02:c207:1:2::ffff", "203.0.113.7"} {
		_ = (&session{s: s, ip: ip}).Mail("p@client.fr", nil)
	}
	if want := []string{"2a02:c207:1:2::/64", "2a02:c207:1:2::/64", "203.0.113.7"}; strings.Join(k.keys, " ") != strings.Join(want, " ") {
		t.Fatalf("clés %v", k.keys)
	}
}

// sendRaw dépose un message tel quel, depuis le 127.0.0.1 du test.
func sendRaw(addr, mailFrom, to string, raw []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(mailFrom); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, _ = w.Write(raw)
	return w.Close()
}

const spoofBody = "To: jc@boite.fr\r\nSubject: Re: x\r\nDate: Wed, 26 Nov 2026 10:00:00 +0000\r\nMessage-Id: <r2@evil.test>\r\n\r\nJe me desinscris\r\n"

// Le port 25 est public : un inconnu qui écrit « From: contact connu » ne déclenche pas le repli « From connu » ; le même mail
// signé DKIM par le domaine du contact le déclenche. Le verdict est posé en tête, celui qu'un expéditeur y met est retiré.
func TestReceive_AuthenticationResults(t *testing.T) {
	forged := "Authentication-Results: mx.test; spf=pass smtp.mailfrom=client.fr; dmarc=pass header.from=client.fr\r\n"
	f, l := &fake{}, &lister{ws: []*domain.Workspace{ws("gbc", "jc@boite.fr")}}
	z := zone()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	z.txt["s1._domainkey.client.fr"] = []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)}
	addr := startAuth(t, f, l, mxauth.New(z))

	// 1. Usurpation : faux verdict en prime, SPF fail, aucune signature.
	if err := sendRaw(addr, "contact@client.fr", "jc@boite.fr", []byte(forged+"From: Contact <contact@client.fr>\r\n"+spoofBody)); err != nil {
		t.Fatal(err)
	}
	got := string(f.raw)
	if n := strings.Count(strings.ToLower(got), "authentication-results: mx.test"); n != 1 {
		t.Fatalf("un seul verdict à notre nom attendu, %d : %q", n, got)
	}
	if strings.Contains(got, "dmarc=pass") || !strings.Contains(strings.ReplaceAll(got, "\r\n\t", " "), "spf=fail") || !strings.Contains(got, "dmarc=fail") {
		t.Fatalf("verdict attendu spf=fail dmarc=fail, le faux est retiré : %q", got)
	}
	if !strings.HasPrefix(got, "Return-Path: <contact@client.fr>\r\nReceived: ") || mxauth.FallbackAllowed(f.msgs[0].RawBody) {
		t.Fatalf("le verdict suit la trace, et le mail usurpé n'autorise pas le repli : %q", got)
	}

	// 2. Le même, signé DKIM par client.fr : DMARC passe, le repli est retenu.
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, strings.NewReader("From: Contact <contact@client.fr>\r\n"+spoofBody), &dkim.SignOptions{Domain: "client.fr", Selector: "s1", Signer: priv}); err != nil {
		t.Fatal(err)
	}
	if err := sendRaw(addr, "bounce@esp.test", "jc@boite.fr", signed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if len(f.msgs) != 2 || !strings.Contains(string(f.raw), "dmarc=pass") || !mxauth.FallbackAllowed(f.msgs[1].RawBody) {
		t.Fatalf("DKIM aligné : repli attendu : %q", f.raw)
	}
}
