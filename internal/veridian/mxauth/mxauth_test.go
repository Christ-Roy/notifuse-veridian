package mxauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/authres"
	"github.com/emersion/go-msgauth/dkim"
)

// dns : un DNS de poche. Aucune requête réseau : un nom absent est NXDOMAIN, down = panne de résolveur.
type dns struct {
	txt  map[string][]string
	ips  map[string][]string
	down bool
	n    int
}

func (d *dns) fail(name string) error {
	d.n++
	if d.down {
		return &net.DNSError{Err: "server misbehaving", Name: name, IsTemporary: true}
	}
	return nil
}

func (d *dns) LookupTXT(_ context.Context, name string) ([]string, error) {
	if err := d.fail(name); err != nil {
		return nil, err
	}
	if v, ok := d.txt[strings.ToLower(name)]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (d *dns) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if err := d.fail(name); err != nil {
		return nil, err
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (d *dns) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if err := d.fail(host); err != nil {
		return nil, err
	}
	var out []net.IPAddr
	for _, s := range d.ips[strings.ToLower(host)] {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	if out == nil {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return out, nil
}
func (d *dns) LookupAddr(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}

const host = "mx.test"

var (
	contactIP = net.ParseIP("198.51.100.7") // le vrai serveur du contact
	evilIP    = net.ParseIP("203.0.113.66")
)

func zone() *dns {
	return &dns{txt: map[string][]string{
		"client.fr":        {"v=spf1 ip4:198.51.100.7 -all"},
		"_dmarc.client.fr": {"v=DMARC1; p=reject"},
		"evil.test":        {"v=spf1 ip4:203.0.113.66 -all"},
		"nopolicy.fr":      {"v=spf1 ip4:198.51.100.7 -all"},
	}}
}

const body = "To: jc@boite.fr\r\nSubject: Re: x\r\nDate: Wed, 26 Nov 2026 10:00:00 +0000\r\nMessage-Id: <r1@client.fr>\r\n\r\nOui\r\n"

func mailFrom(from string) []byte { return []byte("From: Contact <" + from + ">\r\n" + body) }

// sign signe le message avec une clé ed25519 publiée dans le DNS de poche.
func sign(t *testing.T, z *dns, raw []byte, domain string) []byte {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	z.txt["s1._domainkey."+domain] = []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)}
	var out bytes.Buffer
	if err := dkim.Sign(&out, bytes.NewReader(raw), &dkim.SignOptions{Domain: domain, Selector: "s1", Signer: priv}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// stored : le message tel que le récepteur le range (Return-Path, Received, notre verdict, puis le message).
func stored(a *Authenticator, in Input) []byte {
	return []byte("Return-Path: <" + in.MailFrom + ">\r\nReceived: from [x] by mx.test with ESMTP; Wed, 26 Nov 2026 10:00:05 +0000\r\n" +
		a.Header(context.Background(), host, in) + string(in.Raw))
}

func verdict(t *testing.T, a *Authenticator, in Input) (spf, dm string, dk []string) {
	t.Helper()
	for _, r := range a.Check(context.Background(), in) {
		switch r := r.(type) {
		case *authres.SPFResult:
			spf = string(r.Value)
		case *authres.DMARCResult:
			dm = string(r.Value)
		case *authres.DKIMResult:
			dk = append(dk, string(r.Value))
		}
	}
	return
}

// Un inconnu qui écrit « From: contact connu » : SPF fail, pas de DKIM, DMARC fail : le repli faible n'est pas retenu.
func TestSpoofedKnownContact_NoFallback(t *testing.T) {
	a := New(zone())
	in := Input{IP: evilIP, Helo: "evil.test", MailFrom: "x@evil.test", Raw: mailFrom("contact@client.fr")}
	spf, dm, dk := verdict(t, a, in)
	if spf != "pass" || dm != "fail" || len(dk) != 1 || dk[0] != "none" {
		t.Fatalf("spf=%s dmarc=%s dkim=%v : SPF pass sur evil.test, mais non aligné sur client.fr", spf, dm, dk)
	}
	if FallbackAllowed(stored(a, in)) {
		t.Fatal("un mail usurpé ne doit pas déclencher le repli « From connu »")
	}
	// Même usurpation avec un MAIL FROM du domaine visé : SPF fail net.
	in.MailFrom = "contact@client.fr"
	if spf, dm, _ = verdict(t, a, in); spf != "fail" || dm != "fail" {
		t.Fatalf("spf=%s dmarc=%s", spf, dm)
	}
	if FallbackAllowed(stored(a, in)) {
		t.Fatal("SPF fail : repli refusé")
	}
}

// Le même mail, mais signé DKIM par le domaine du From : DMARC passe, le repli est retenu.
func TestSignedByTheFromDomain_FallbackAllowed(t *testing.T) {
	z := zone()
	a := New(z)
	in := Input{IP: evilIP, Helo: "relais.test", MailFrom: "bounce@esp.test", Raw: sign(t, z, mailFrom("contact@client.fr"), "client.fr")}
	spf, dm, dk := verdict(t, a, in)
	if spf == "pass" || dm != "pass" || len(dk) != 1 || dk[0] != "pass" {
		t.Fatalf("spf=%s dmarc=%s dkim=%v", spf, dm, dk)
	}
	if !FallbackAllowed(stored(a, in)) {
		t.Fatal("DKIM aligné valide : le repli doit être retenu")
	}
	// Une signature valide d'un autre domaine ne vaut rien pour client.fr.
	z.txt["_dmarc.evil.test"] = []string{"v=DMARC1; p=none"}
	in.Raw = sign(t, z, mailFrom("contact@client.fr"), "evil.test")
	if _, dm, dk = verdict(t, a, in); dm != "fail" || dk[0] != "pass" {
		t.Fatalf("dmarc=%s dkim=%v : signature valide mais non alignée", dm, dk)
	}
	if FallbackAllowed(stored(a, in)) {
		t.Fatal("DKIM non aligné : repli refusé")
	}
	// Un message modifié après signature ne passe plus.
	in.Raw = bytes.Replace(sign(t, z, mailFrom("contact@client.fr"), "client.fr"), []byte("Oui"), []byte("Non"), 1)
	if _, dm, dk = verdict(t, a, in); dm != "fail" || dk[0] == "pass" {
		t.Fatalf("dmarc=%s dkim=%v : corps altéré", dm, dk)
	}
}

// SPF aligné (MAIL FROM du domaine du From, IP autorisée) : DMARC passe. Domaine organisationnel : un sous-domaine suit.
func TestSPFAligned_AndOrganizationalDomain(t *testing.T) {
	a := New(zone())
	in := Input{IP: contactIP, Helo: "smtp.client.fr", MailFrom: "contact@client.fr", Raw: mailFrom("contact@client.fr")}
	if spf, dm, _ := verdict(t, a, in); spf != "pass" || dm != "pass" {
		t.Fatalf("spf=%s dmarc=%s", spf, dm)
	}
	if !FallbackAllowed(stored(a, in)) {
		t.Fatal("SPF aligné : repli retenu")
	}
	in.Raw = mailFrom("contact@rh.client.fr") // politique lue sur client.fr, alignement souple
	in.MailFrom = "contact@client.fr"
	if _, dm, _ := verdict(t, a, in); dm != "pass" {
		t.Fatalf("sous-domaine : dmarc=%s", dm)
	}
}

// Sans politique DMARC publiée : SPF ou DKIM aligné qui passe suffit, sinon non.
func TestNoDMARCPolicy_FallsBackOnAlignedSPFOrDKIM(t *testing.T) {
	z := zone()
	a := New(z)
	in := Input{IP: contactIP, Helo: "x", MailFrom: "c@nopolicy.fr", Raw: mailFrom("c@nopolicy.fr")}
	if _, dm, _ := verdict(t, a, in); dm != "none" {
		t.Fatalf("dmarc=%s", dm)
	}
	if !FallbackAllowed(stored(a, in)) {
		t.Fatal("pas de politique, SPF aligné : repli retenu")
	}
	in.IP = evilIP // SPF fail et pas de DKIM
	if FallbackAllowed(stored(a, in)) {
		t.Fatal("pas de politique, SPF fail, pas de DKIM : repli refusé")
	}
	in.Raw = sign(t, z, mailFrom("c@nopolicy.fr"), "nopolicy.fr")
	if !FallbackAllowed(stored(a, in)) {
		t.Fatal("pas de politique, DKIM aligné : repli retenu")
	}
}

// DNS en panne : aucun refus (Header rend toujours un verdict), temperror, repli non retenu.
func TestDNSDown_TempErrorAndNoFallback(t *testing.T) {
	z := zone()
	raw := sign(t, z, mailFrom("contact@client.fr"), "client.fr")
	z.down = true
	a := New(z)
	in := Input{IP: contactIP, Helo: "x", MailFrom: "contact@client.fr", Raw: raw}
	spf, dm, dk := verdict(t, a, in)
	if spf != "temperror" || dm != "temperror" || dk[0] != "temperror" {
		t.Fatalf("spf=%s dmarc=%s dkim=%v", spf, dm, dk)
	}
	h := stored(a, in)
	if !strings.Contains(string(h), "Authentication-Results: mx.test;") || FallbackAllowed(h) {
		t.Fatalf("en-tête posé et repli refusé attendus : %s", h)
	}
}

// L'en-tête : posé à notre nom, lisible, relu à l'identique ; un From répété n'est pas évalué.
func TestHeaderFormat_AndRepeatedFrom(t *testing.T) {
	a := New(zone())
	in := Input{IP: contactIP, Helo: "smtp.client.fr", MailFrom: "contact@client.fr", Raw: mailFrom("contact@client.fr")}
	h := a.Header(context.Background(), host, in)
	for _, w := range []string{"Authentication-Results: mx.test;", "spf=pass", "smtp.mailfrom=contact@client.fr", "dkim=none", "dmarc=pass", "header.from=client.fr"} {
		if !strings.Contains(strings.ReplaceAll(h, "\r\n\t", " "), w) {
			t.Errorf("manque %q dans %q", w, h)
		}
	}
	if !strings.HasSuffix(h, "\r\n") || strings.Count(h, "\r\n\r\n") != 0 {
		t.Fatalf("une seule ligne d'en-tête, terminée par CRLF : %q", h)
	}
	in.Raw = []byte("From: a@client.fr\r\nFrom: b@client.fr\r\n" + body)
	if _, dm, _ := verdict(t, a, in); dm != "permerror" {
		t.Fatalf("From répété : dmarc=%s", dm)
	}
	if FallbackAllowed(stored(a, in)) {
		t.Fatal("From répété : repli refusé")
	}
}

// Un Authentication-Results de notre nom venu de l'expéditeur est retiré (replié compris), ceux des autres noms et le reste restent.
func TestStripOwn_RemovesForgedHeaders(t *testing.T) {
	raw := []byte("Authentication-Results: mx.test; dmarc=pass header.from=client.fr\r\n" +
		"From: contact@client.fr\r\n" +
		"AUTHENTICATION-RESULTS: MX.TEST 1;\r\n\tspf=pass smtp.mailfrom=client.fr\r\n" +
		"Authentication-Results: mail.google.com; dkim=pass\r\n" +
		"Subject: x\r\n\r\nAuthentication-Results: mx.test; dans le corps\r\n")
	got := string(StripOwn(raw, host))
	want := "From: contact@client.fr\r\nAuthentication-Results: mail.google.com; dkim=pass\r\nSubject: x\r\n\r\nAuthentication-Results: mx.test; dans le corps\r\n"
	if got != want {
		t.Fatalf("obtenu :\n%q\nattendu :\n%q", got, want)
	}
	if string(StripOwn([]byte("Subject: sans corps\r\n"), host)) != "Subject: sans corps\r\n" {
		t.Fatal("message sans corps intact")
	}
}

// Un faux verdict qui n'est pas posé par le récepteur (ailleurs dans l'en-tête, ou mal formé) ne rend jamais le repli.
func TestFallbackAllowed_IgnoresForgedVerdicts(t *testing.T) {
	forged := "Authentication-Results: mx.test; spf=pass smtp.mailfrom=client.fr; dmarc=pass header.from=client.fr\r\n"
	for name, raw := range map[string]string{
		"après d'autres en-têtes": "Return-Path: <x@evil.test>\r\nReceived: a\r\nReceived: b\r\n" + forged + "From: c@client.fr\r\n\r\nx",
		"après le From":           "Return-Path: <x@evil.test>\r\nFrom: c@client.fr\r\n" + forged + "\r\nx",
		"absent":                  "Return-Path: <x@evil.test>\r\nReceived: a\r\nFrom: c@client.fr\r\n\r\nx",
		"illisible":               "Return-Path: <x@evil.test>\r\nReceived: a\r\nAuthentication-Results: ;;;===\r\nFrom: c@client.fr\r\n\r\nx",
		"dmarc fail":              "Return-Path: <x>\r\nReceived: a\r\nAuthentication-Results: mx.test; dmarc=fail header.from=client.fr\r\n\r\nx",
		"dmarc none sans domaine": "Return-Path: <x>\r\nReceived: a\r\nAuthentication-Results: mx.test; spf=pass smtp.mailfrom=client.fr; dmarc=none\r\n\r\nx",
		"dmarc none non aligné":   "Return-Path: <x>\r\nReceived: a\r\nAuthentication-Results: mx.test; spf=pass smtp.mailfrom=evil.test; dmarc=none header.from=client.fr\r\n\r\nx",
	} {
		if FallbackAllowed([]byte(raw)) {
			t.Errorf("%s : repli retenu à tort", name)
		}
	}
	ok := "Return-Path: <x>\r\nReceived: a\r\nAuthentication-Results: mx.test; dmarc=pass header.from=client.fr\r\nFrom: c@client.fr\r\n\r\nx"
	if !FallbackAllowed([]byte(ok)) {
		t.Fatal("verdict posé par le récepteur : repli retenu")
	}
}

func TestIsReceivedAndAligned(t *testing.T) {
	if !IsReceived("mx:p1") || IsReceived("imap:p1") || IsReceived("mx") {
		t.Fatal("identifiant d'intégration du récepteur")
	}
	if !Aligned("mail.client.fr", "client.fr", false) || Aligned("mail.client.fr", "client.fr", true) || Aligned("evil.test", "client.fr", false) || Aligned("", "client.fr", false) {
		t.Fatal("alignement souple et strict")
	}
	if Aligned("a.co.uk", "b.co.uk", false) {
		t.Fatal("co.uk est un suffixe public, pas un domaine organisationnel")
	}
}
