package boxsend

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	gosmtp "github.com/emersion/go-smtp"
)

func startGate(t *testing.T, s *Sender) string {
	t.Helper()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy(LimitGateIP, GateTriesIP, GateWindow)
	rl.SetPolicy(LimitGateBox, GateTriesBox, GateWindow)
	t.Cleanup(rl.Stop)
	g, err := NewGate(s, rl, "smtp.test")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = g.Serve(ln) }()
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return ln.Addr().String()
}

func TestGate_SendsWithBoxAccess(t *testing.T) {
	f, s := bench()
	c, err := smtp.Dial(startGate(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Mail("jc@boite.fr"); err == nil {
		t.Fatal("envoi sans authentification")
	}
	if err := c.Auth(smtp.PlainAuth("", "jc@boite.fr", "bon", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("jc@boite.fr"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("p@client.fr"); err != nil {
		t.Fatal(err)
	}
	w, _ := c.Data()
	_, _ = w.Write(msg("jc@boite.fr", "To: p@client.fr\r\n"))
	if err := w.Close(); err != nil || len(f.sent) != 1 || f.sent[0].To != "p@client.fr" {
		t.Fatalf("%v %+v", err, f.sent)
	}
}

func TestGate_RefusesAPIKeyAndBoundsTries(t *testing.T) {
	f, s := bench()
	addr := startGate(t, s)
	c, _ := smtp.Dial(addr)
	if err := c.Auth(smtp.PlainAuth("", "cle@api.ws", "aa.bb.cc", "127.0.0.1")); err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("clé d'API : %v", err)
	}
	c.Close()
	f.verify = boxaccess.ErrAuth
	codes := map[string]int{}
	for i := 0; i < GateTriesIP+3; i++ {
		c, err := smtp.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Auth(smtp.PlainAuth("", fmt.Sprintf("boite%d@boite.fr", i), "faux", "127.0.0.1")); err != nil {
			codes[err.Error()[:3]]++
		}
		c.Close()
	}
	if codes["535"] != GateTriesIP-1 || codes["454"] != 4 {
		t.Fatalf("essais par adresse : %v", codes)
	}
}

// AUTH LOGIN (vieux Outlook), et au plus trois essais par connexion.
func TestGate_AuthLoginAndTriesPerConnection(t *testing.T) {
	f, s := bench()
	conn, err := net.Dial("tcp", startGate(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	line := func() string {
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if len(l) > 3 && l[3] == ' ' {
				return strings.TrimSpace(l)
			}
		}
	}
	say := func(x string) string { fmt.Fprintf(conn, "%s\r\n", x); return line() }
	b := func(x string) string { return base64.StdEncoding.EncodeToString([]byte(x)) }
	line()
	say("EHLO poste")
	f.verify = boxaccess.ErrAuth
	try := func() string {
		if l := say("AUTH LOGIN"); !strings.HasPrefix(l, "334") {
			return l
		}
		say(b("jc@boite.fr"))
		return say(b("faux"))
	}
	for i := 0; i < triesPerConn; i++ {
		if l := try(); !strings.HasPrefix(l, "535") {
			t.Fatalf("essai %d : %s", i, l)
		}
	}
	if l := try(); !strings.HasPrefix(l, "454") {
		t.Fatalf("quatrième essai sur la même connexion : %s", l)
	}
	f.verify = nil
	conn2, _ := net.Dial("tcp", conn.RemoteAddr().String())
	defer conn2.Close()
	conn, r = conn2, bufio.NewReader(conn2)
	line()
	say("EHLO poste")
	if l := try(); !strings.HasPrefix(l, "235") {
		t.Fatalf("AUTH LOGIN : %s", l)
	}
}

func TestNewGate_Dependencies(t *testing.T) {
	if _, err := NewGate(nil, nil, ""); err == nil {
		t.Fatal("dépendances manquantes")
	}
}

// Une boîte visée depuis une adresse se ferme pour CETTE adresse (même avec le bon mot de passe), pas pour les autres boîtes.
func TestGate_BoundsTriesPerBoxAndAddress(t *testing.T) {
	f, s := bench()
	addr := startGate(t, s)
	auth := func(box string) string {
		c, err := smtp.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.Auth(smtp.PlainAuth("", box, "x", "127.0.0.1")); err != nil {
			return err.Error()[:3]
		}
		return "235"
	}
	f.verify = boxaccess.ErrAuth
	for i := 0; i < GateTriesBox; i++ {
		if got := auth("jc@boite.fr"); got != "535" {
			t.Fatalf("essai %d : %s", i, got)
		}
	}
	f.verify = nil
	if got := auth("JC@boite.fr"); got != "454" {
		t.Fatalf("boîte visée, casse différente : %s", got)
	}
	if got := auth("autre@boite.fr"); got != "235" {
		t.Fatalf("autre boîte depuis la même adresse : %s", got)
	}
}

// En IPv6 un client dispose d'un /64 entier : changer d'adresse dedans ne rend pas un compteur neuf.
func TestGate_IPv6LimitedByPrefix(t *testing.T) {
	f, s := bench()
	f.verify = boxaccess.ErrAuth
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy(LimitGateIP, GateTriesIP, GateWindow)
	rl.SetPolicy(LimitGateBox, GateTriesBox, GateWindow)
	t.Cleanup(rl.Stop)
	g, err := NewGate(s, rl, "smtp.test")
	if err != nil {
		t.Fatal(err)
	}
	code := func(ip string) int {
		var se *gosmtp.SMTPError
		if !errors.As((&gateSession{g: g, ip: ip}).login("jc@boite.fr", "faux"), &se) {
			t.Fatal("un mauvais mot de passe est refusé")
		}
		return se.Code
	}
	last := 0
	for i := 0; i <= GateTriesBox; i++ {
		last = code(fmt.Sprintf("2a02:c207:1:2::%x", i+1))
	}
	if last != 454 {
		t.Fatalf("même /64 : %d, attendu 454", last)
	}
	if c := code("2a02:c207:1:3::1"); c != 535 {
		t.Fatalf("autre /64 : %d, attendu 535", c)
	}
}

func selfSigned(t *testing.T, name string) tls.Certificate {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

// Soumission 587 : ni AUTH ni envoi avant STARTTLS, les mêmes règles après.
func TestGate_ServeStartTLS(t *testing.T) {
	f, s := bench()
	rl := ratelimiter.NewRateLimiter()
	rl.SetPolicy(LimitGateIP, GateTriesIP, GateWindow)
	rl.SetPolicy(LimitGateBox, GateTriesBox, GateWindow)
	t.Cleanup(rl.Stop)
	g, err := NewGate(s, rl, "smtp.test")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ServeStartTLS(ln); err == nil {
		t.Fatal("587 servi sans avoir été préparé")
	}
	for _, tc := range []*tls.Config{nil, {}} {
		if err := g.EnableStartTLS(tc); err == nil {
			t.Fatal("587 sans certificat")
		}
	}
	if err := g.EnableStartTLS(&tls.Config{Certificates: []tls.Certificate{selfSigned(t, "smtp.test")}, MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = g.ServeStartTLS(ln) }()
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	dial := func() *smtp.Client {
		c, err := smtp.Dial(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := dial()
	if ok, _ := c.Extension("AUTH"); ok {
		t.Fatal("AUTH annoncé avant TLS : le mot de passe passerait en clair")
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		t.Fatal("STARTTLS non annoncé")
	}
	if err := c.Auth(clearAuth{}); err == nil {
		t.Fatal("AUTH accepté en clair")
	}
	if err := c.Mail("jc@boite.fr"); err == nil {
		t.Fatal("envoi sans authentification")
	}
	c.Close()
	c = dial()
	defer c.Close()
	if err := c.StartTLS(&tls.Config{ServerName: "smtp.test", InsecureSkipVerify: true}); err != nil { //nolint:gosec // certificat de test
		t.Fatal(err)
	}
	if err := c.Auth(smtp.PlainAuth("", "jc@boite.fr", "bon", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("jc@boite.fr"); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("p@client.fr"); err != nil {
		t.Fatal(err)
	}
	w, _ := c.Data()
	_, _ = w.Write(msg("jc@boite.fr", "To: p@client.fr\r\n"))
	if err := w.Close(); err != nil || len(f.sent) != 1 {
		t.Fatalf("%v %+v", err, f.sent)
	}
}

// clearAuth : un client qui insiste sans TLS.
type clearAuth struct{}

func (clearAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00jc@boite.fr\x00bon"), nil
}
func (clearAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }
