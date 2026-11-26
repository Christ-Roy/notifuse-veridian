// Package mxauth authentifie le courrier reçu sur le port 25 public (fiche 84, « Authentification du courrier entrant ») :
// SPF de l'adresse réelle du client, DKIM des signatures du message, alignement DMARC du domaine de l'en-tête From. Le verdict
// est posé dans un en-tête Authentication-Results (RFC 8601) qui porte NOTRE nom d'hôte ; tout en-tête de ce nom venu de
// l'expéditeur est retiré avant. Le détecteur de réponses ne retient le repli faible « From = contact connu » que si ce verdict
// le permet (FallbackAllowed). Le DNS est injectable : les tests ne font aucune requête réseau. Un DNS en panne ne fait jamais
// refuser un mail : verdict temperror, le mail est rangé, le repli faible n'est pas retenu.
package mxauth

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/authres"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
	"golang.org/x/net/publicsuffix"
)

// SourceID : boîte source portée par un mail reçu directement sur le port 25 (pas d'intégration IMAP). L'identifiant d'intégration
// d'un tel mail est SourceID + ":" + profil.
const SourceID = "mx"

// IsReceived dit si l'identifiant d'intégration d'un message est celui du récepteur du port 25.
func IsReceived(integrationID string) bool { return strings.HasPrefix(integrationID, SourceID+":") }

// Resolver : le DNS (compatible *net.Resolver).
type Resolver = spf.DNSResolver

// Bornes.
const (
	checkTimeout  = 20 * time.Second
	maxSignatures = 5
	headerName    = "Authentication-Results"
)

// Input : ce que le récepteur sait d'un message au moment de le ranger.
type Input struct {
	IP       net.IP // adresse réelle du client (PROXY v2), nil si inconnue
	Helo     string
	MailFrom string // enveloppe, vide pour un rapport de non-remise
	Raw      []byte // message tel que reçu
}

// Authenticator évalue SPF, DKIM et DMARC.
type Authenticator struct{ resolver Resolver }

// New construit l'évaluateur ; resolver nil : le DNS du système.
func New(resolver Resolver) *Authenticator {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Authenticator{resolver: resolver}
}

// Header rend la ligne « Authentication-Results: <host>; spf=...; dkim=...; dmarc=... » (CRLF final compris). Elle ne rend jamais
// d'erreur : ce qui n'a pas pu être mesuré est un temperror ou permerror dans le verdict.
func (a *Authenticator) Header(ctx context.Context, host string, in Input) string {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	v := authres.Format(clean(host), a.Check(ctx, in))
	return headerName + ": " + strings.ReplaceAll(v, "; ", ";\r\n\t") + "\r\n"
}

// Check rend les verdicts SPF, DKIM (un par signature, none sans signature) et DMARC.
func (a *Authenticator) Check(ctx context.Context, in Input) []authres.Result {
	spfRes := a.spf(ctx, in)
	dkimRes, verifs := a.dkim(ctx, in.Raw)
	out := []authres.Result{spfRes}
	out = append(out, dkimRes...)
	return append(out, a.dmarc(ctx, in.Raw, spfRes, verifs))
}

func (a *Authenticator) spf(ctx context.Context, in Input) *authres.SPFResult {
	helo, sender := clean(in.Helo), clean(in.MailFrom)
	if sender == "" && helo != "" {
		sender = "postmaster@" + helo // RFC 7208 §2.4 : rapport de non-remise, l'identité est le HELO
	}
	res := &authres.SPFResult{From: sender, Helo: helo}
	if in.IP == nil || sender == "" {
		res.Value, res.Reason = authres.ResultNone, "no client address or identity"
		return res
	}
	r, _ := spf.CheckHostWithSender(in.IP, helo, sender, spf.WithContext(ctx), spf.WithResolver(a.resolver))
	res.Value = authres.ResultValue(r)
	return res
}

func (a *Authenticator) dkim(ctx context.Context, raw []byte) ([]authres.Result, []*dkim.Verification) {
	verifs, err := dkim.VerifyWithOptions(bytes.NewReader(crlf(raw)), &dkim.VerifyOptions{
		MaxVerifications: maxSignatures,
		LookupTXT:        func(name string) ([]string, error) { return a.resolver.LookupTXT(ctx, name) },
	})
	if len(verifs) == 0 {
		if err != nil && !errors.Is(err, dkim.ErrTooManySignatures) {
			return []authres.Result{&authres.DKIMResult{Value: authres.ResultPermError, Reason: "unreadable message"}}, nil
		}
		return []authres.Result{&authres.DKIMResult{Value: authres.ResultNone}}, nil
	}
	out := make([]authres.Result, 0, len(verifs))
	for _, v := range verifs {
		r := &authres.DKIMResult{Domain: clean(v.Domain), Identifier: clean(v.Identifier)}
		switch {
		case v.Err == nil:
			r.Value = authres.ResultPass
		case dkim.IsTempFail(v.Err):
			r.Value, r.Reason = authres.ResultTempError, short(v.Err)
		case dkim.IsPermFail(v.Err):
			r.Value, r.Reason = authres.ResultPermError, short(v.Err)
		default:
			r.Value, r.Reason = authres.ResultFail, short(v.Err)
		}
		out = append(out, r)
	}
	return out, verifs
}

func (a *Authenticator) dmarc(ctx context.Context, raw []byte, spfRes *authres.SPFResult, verifs []*dkim.Verification) *authres.DMARCResult {
	from, why := fromDomain(raw)
	if why != "" {
		return &authres.DMARCResult{Value: authres.ResultPermError, Reason: why}
	}
	res := &authres.DMARCResult{From: from}
	rec, state, why := a.policy(ctx, from)
	if state != "" {
		res.Value, res.Reason = state, why
		return res
	}
	if rec == nil {
		res.Value, res.Reason = authres.ResultNone, "no DMARC policy published"
		return res
	}
	spfAligned := spfRes.Value == authres.ResultPass && Aligned(domainOf(spfRes.From), from, rec.SPFAlignment == dmarc.AlignmentStrict)
	dkimAligned, temp := false, spfRes.Value == authres.ResultTempError
	for _, v := range verifs {
		if v.Err == nil && Aligned(v.Domain, from, rec.DKIMAlignment == dmarc.AlignmentStrict) {
			dkimAligned = true
		}
		temp = temp || dkim.IsTempFail(v.Err)
	}
	switch {
	case spfAligned || dkimAligned:
		res.Value = authres.ResultPass
	case temp:
		res.Value, res.Reason = authres.ResultTempError, "dns failure during SPF or DKIM"
	default:
		res.Value = authres.ResultFail
	}
	return res
}

// policy cherche la politique du domaine, puis celle du domaine organisationnel (RFC 7489 §6.6.3). Rien de publié : (nil, "", "").
func (a *Authenticator) policy(ctx context.Context, domain string) (*dmarc.Record, authres.ResultValue, string) {
	opts := &dmarc.LookupOptions{LookupTXT: func(name string) ([]string, error) { return a.resolver.LookupTXT(ctx, name) }}
	rec, err := dmarc.LookupWithOptions(domain, opts)
	if errors.Is(err, dmarc.ErrNoPolicy) {
		if org := orgDomain(domain); org != "" && org != domain {
			rec, err = dmarc.LookupWithOptions(org, opts)
		}
	}
	switch {
	case err == nil:
		return rec, "", ""
	case errors.Is(err, dmarc.ErrNoPolicy):
		return nil, "", ""
	case dmarc.IsTempFail(err) || strings.Contains(err.Error(), "failed to lookup"):
		return nil, authres.ResultTempError, "dns failure reading the DMARC policy"
	}
	return nil, authres.ResultPermError, "unreadable DMARC policy"
}

// fromDomain : le domaine de l'en-tête From. Absent, répété ou avec plusieurs domaines : refus d'évaluer (raison rendue).
func fromDomain(raw []byte) (string, string) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", "unreadable header"
	}
	if len(msg.Header["From"]) != 1 {
		return "", "From header missing or repeated"
	}
	list, err := msg.Header.AddressList("From")
	if err != nil || len(list) == 0 {
		return "", "unreadable From header"
	}
	d := ""
	for _, ad := range list {
		x := domainOf(ad.Address)
		if x == "" || (d != "" && x != d) {
			return "", "From header with no domain or several domains"
		}
		d = x
	}
	return d, ""
}

func domainOf(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(addr[i+1:]), "."))
}

func orgDomain(d string) string {
	o, err := publicsuffix.EffectiveTLDPlusOne(d)
	if err != nil {
		return d
	}
	return o
}

// Aligned : le domaine a est aligné sur le domaine b de l'en-tête From (strict : identiques ; souple : même domaine organisationnel).
func Aligned(a, b string, strict bool) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if a == "" || b == "" {
		return false
	}
	if strict {
		return a == b
	}
	return orgDomain(a) == orgDomain(b)
}

// crlf : le DKIM se calcule sur des fins de ligne CRLF.
func crlf(raw []byte) []byte {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))
}

// clean ôte ce qui casserait un en-tête (contrôles, espaces).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 33 || r == 127 || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func short(err error) string {
	s := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, err.Error())
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// ---------- en-têtes ----------

type field struct {
	name string
	raw  []byte // ligne(s) brute(s), fins de ligne comprises
}

func (f field) value() string {
	i := bytes.IndexByte(f.raw, ':')
	if i < 0 {
		return ""
	}
	v := strings.NewReplacer("\r\n", "", "\n", "", "\t", " ").Replace(string(f.raw[i+1:]))
	return strings.TrimSpace(v)
}

// fields découpe l'en-tête (champs repliés regroupés) et rend aussi l'offset de la ligne vide qui précède le corps.
func fields(raw []byte) ([]field, int) {
	var out []field
	i := 0
	for i < len(raw) {
		e := len(raw)
		if j := bytes.IndexByte(raw[i:], '\n'); j >= 0 {
			e = i + j + 1
		}
		line := raw[i:e]
		if len(bytes.TrimRight(line, "\r\n")) == 0 {
			return out, i // début de la ligne vide
		}
		if (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			out[len(out)-1].raw = append(out[len(out)-1].raw, line...)
		} else {
			name := ""
			if k := bytes.IndexByte(line, ':'); k > 0 {
				name = strings.TrimSpace(string(line[:k]))
			}
			out = append(out, field{name: name, raw: append([]byte(nil), line...)})
		}
		i = e
	}
	return out, len(raw)
}

// authservID : l'identifiant du serveur qui a posé un Authentication-Results.
func authservID(value string) string {
	if i := strings.IndexAny(value, "; \t"); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value)
}

// StripOwn retire les en-têtes Authentication-Results dont l'identifiant est le nôtre : venus de l'expéditeur, ils sont des faux.
func StripOwn(raw []byte, host string) []byte {
	fs, body := fields(raw)
	var out bytes.Buffer
	for _, f := range fs {
		if strings.EqualFold(f.name, headerName) && strings.EqualFold(authservID(f.value()), clean(host)) {
			continue
		}
		out.Write(f.raw)
	}
	out.Write(raw[body:])
	return out.Bytes()
}

// FallbackAllowed dit si le verdict que NOUS avons posé autorise le repli faible « From = contact connu » : DMARC pass ; ou, sans
// politique DMARC publiée, un SPF ou un DKIM qui passe aligné sur le domaine du From. Absent, mal placé ou illisible : non.
// Seul compte le premier Authentication-Results, placé par le récepteur juste après Return-Path et Received (StripOwn a retiré
// ceux de l'expéditeur qui portaient notre nom ; ceux des autres noms viennent après le nôtre).
func FallbackAllowed(raw []byte) bool {
	fs, _ := fields(raw)
	for i, f := range fs {
		if !strings.EqualFold(f.name, headerName) {
			if i < 2 && (strings.EqualFold(f.name, "Return-Path") || strings.EqualFold(f.name, "Received")) {
				continue
			}
			return false
		}
		return verdictAllows(f.value())
	}
	return false
}

func verdictAllows(value string) bool {
	_, results, err := authres.Parse(value)
	if err != nil {
		return false
	}
	var dm *authres.DMARCResult
	var spfPass, dkimPass []string
	for _, r := range results {
		switch r := r.(type) {
		case *authres.DMARCResult:
			dm = r
		case *authres.SPFResult:
			if r.Value == authres.ResultPass {
				spfPass = append(spfPass, domainOf(r.From))
			}
		case *authres.DKIMResult:
			if r.Value == authres.ResultPass {
				dkimPass = append(dkimPass, r.Domain)
			}
		}
	}
	switch {
	case dm == nil:
		return false
	case dm.Value == authres.ResultPass:
		return true
	case dm.Value == authres.ResultNone && dm.From != "":
		for _, d := range append(spfPass, dkimPass...) {
			if Aligned(d, dm.From, false) {
				return true
			}
		}
	}
	return false
}
