package domauth

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"blitiri.com.ar/go/spf"
	"golang.org/x/net/publicsuffix"
)

// Resolver : le DNS (compatible *net.Resolver). Injectable : les tests ne font aucune requête réseau.
type Resolver = spf.DNSResolver

// Verifier relit le DNS d'un domaine d'envoi.
type Verifier struct {
	Resolver Resolver
	// Include : notre enregistrement SPF (DefaultSPFInclude). Ses mécanismes ip4/ip6 sont les adresses de nos relais.
	Include string
	Timeout time.Duration
}

func (v *Verifier) resolver() Resolver {
	if v.Resolver == nil {
		return net.DefaultResolver
	}
	return v.Resolver
}

func notFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

// lookup : TXT d'un nom. (nil, nil) = le nom n'a rien ; erreur = DNS injoignable (temperror, jamais un « absent »).
func (v *Verifier) lookup(ctx context.Context, name string) ([]string, error) {
	txt, err := v.resolver().LookupTXT(ctx, name)
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return txt, nil
}

// Verify : un verdict par enregistrement, dans l'ordre DKIM, SPF, DMARC.
func (v *Verifier) Verify(ctx context.Context, d Domain) []Check {
	t := v.Timeout
	if t == 0 {
		t = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	return []Check{v.dkim(ctx, d), v.spf(ctx, d), v.dmarc(ctx, d)}
}

func temp(kind string, err error) Check {
	return Check{Kind: kind, Verdict: "temperror", Detail: "DNS injoignable, rien n'est conclu : réessayez (" + shortErr(err) + ")"}
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// tagValue lit un tag d'une liste « k=v; k=v » (RFC 6376 3.2), espaces retirés.
func tagValue(rec, tag string) (string, bool) {
	for _, part := range strings.Split(rec, ";") {
		k, val, ok := strings.Cut(part, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), tag) {
			return strings.Join(strings.Fields(val), ""), true
		}
	}
	return "", false
}

func (v *Verifier) dkim(ctx context.Context, d Domain) Check {
	txt, err := v.lookup(ctx, DKIMName(d))
	if err != nil {
		return temp(KindDKIM, err)
	}
	if len(txt) == 0 {
		return Check{Kind: KindDKIM, Verdict: "missing", Detail: "aucun enregistrement TXT à " + DKIMName(d)}
	}
	for _, rec := range txt {
		if p, ok := tagValue(rec, "p"); ok && p == d.PublicKey {
			return Check{Kind: KindDKIM, OK: true, Verdict: "pass", Detail: "clé publique publiée et conforme"}
		}
	}
	return Check{Kind: KindDKIM, Verdict: "mismatch", Found: clip(txt), Detail: "un TXT existe mais ne porte pas notre clé publique (p=) : recopiez la valeur entière"}
}

func clip(l []string) []string {
	out := make([]string, 0, len(l))
	for _, s := range l {
		if len(s) > 300 {
			s = s[:300] + "…"
		}
		out = append(out, s)
	}
	return out
}

func spfRecords(txt []string) []string {
	var out []string
	for _, r := range txt {
		f := strings.Fields(strings.ToLower(r))
		if len(f) > 0 && f[0] == "v=spf1" {
			out = append(out, r)
		}
	}
	return out
}

// relayIPs : les adresses listées par notre include (ip4:, ip6:). Vide si l'include est illisible.
func (v *Verifier) relayIPs(ctx context.Context) ([]net.IP, error) {
	txt, err := v.lookup(ctx, v.Include)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, r := range spfRecords(txt) {
		for _, f := range strings.Fields(r) {
			lf := strings.ToLower(f)
			for _, p := range []string{"ip4:", "+ip4:", "ip6:", "+ip6:"} {
				if strings.HasPrefix(lf, p) {
					a := f[len(p):]
					if ip, _, err := net.ParseCIDR(a); err == nil {
						ips = append(ips, ip)
					} else if ip := net.ParseIP(a); ip != nil {
						ips = append(ips, ip)
					}
				}
			}
		}
	}
	return ips, nil
}

func (v *Verifier) spf(ctx context.Context, d Domain) Check {
	txt, err := v.lookup(ctx, d.Domain)
	if err != nil {
		return temp(KindSPF, err)
	}
	recs := spfRecords(txt)
	switch len(recs) {
	case 0:
		return Check{Kind: KindSPF, Verdict: "missing", Detail: "aucun SPF (v=spf1) sur " + d.Domain}
	case 1:
	default:
		return Check{Kind: KindSPF, Verdict: "multiple", Found: clip(recs), Detail: "plusieurs SPF : les serveurs les ignorent tous (permerror), fusionnez-les en un seul"}
	}
	hasInclude := false
	for _, f := range strings.Fields(strings.ToLower(recs[0])) {
		if strings.TrimPrefix(f, "+") == "include:"+strings.ToLower(v.Include) {
			hasInclude = true
		}
	}
	// La preuve qui compte : chaque adresse de nos relais passe le SPF du domaine (include, ip4 recopiée, redirect : peu importe la forme).
	ips, ierr := v.relayIPs(ctx)
	if ierr != nil || len(ips) == 0 {
		if hasInclude {
			return Check{Kind: KindSPF, OK: true, Verdict: "pass", Found: clip(recs), Detail: "notre include est présent (adresses des relais non relues : " + v.Include + ")"}
		}
		if ierr != nil {
			return temp(KindSPF, ierr)
		}
		return Check{Kind: KindSPF, Verdict: "mismatch", Found: clip(recs), Detail: "le SPF n'inclut pas " + v.Include}
	}
	for _, ip := range ips {
		res, err := spf.CheckHostWithSender(ip, d.Domain, "postmaster@"+d.Domain, spf.WithContext(ctx), spf.WithResolver(v.resolver()))
		if res == spf.TempError {
			return temp(KindSPF, errOr(err, "temperror"))
		}
		if res != spf.Pass {
			return Check{Kind: KindSPF, Verdict: "mismatch", Found: clip(recs),
				Detail: "le SPF ne permet pas à notre relais " + ip.String() + " d'envoyer (" + string(res) + ") : ajoutez include:" + v.Include}
		}
	}
	return Check{Kind: KindSPF, OK: true, Verdict: "pass", Found: clip(recs), Detail: "nos relais sont autorisés"}
}

func errOr(err error, s string) error {
	if err != nil {
		return err
	}
	return errors.New(s)
}

func dmarcRecord(txt []string) string {
	for _, r := range txt {
		if strings.HasPrefix(strings.ToLower(strings.Join(strings.Fields(r), "")), "v=dmarc1") {
			return r
		}
	}
	return ""
}

func (v *Verifier) dmarc(ctx context.Context, d Domain) Check {
	txt, err := v.lookup(ctx, "_dmarc."+d.Domain)
	if err != nil {
		return temp(KindDMARC, err)
	}
	if rec := dmarcRecord(txt); rec != "" {
		p, _ := tagValue(rec, "p")
		return Check{Kind: KindDMARC, OK: true, Verdict: "pass", Found: []string{rec}, Detail: "politique publiée (p=" + p + ")"}
	}
	if org, err := publicsuffix.EffectiveTLDPlusOne(d.Domain); err == nil && org != d.Domain {
		otxt, err := v.lookup(ctx, "_dmarc."+org)
		if err != nil {
			return temp(KindDMARC, err)
		}
		if rec := dmarcRecord(otxt); rec != "" {
			p, _ := tagValue(rec, "p")
			return Check{Kind: KindDMARC, OK: true, Verdict: "inherited", Found: []string{rec}, Detail: "politique héritée de " + org + " (p=" + p + ")"}
		}
	}
	return Check{Kind: KindDMARC, Verdict: "missing", Detail: "aucune politique DMARC : recommandé, sans elle Gmail et Yahoo trient plus sévèrement"}
}

// StatusOf : vérifié dès que DKIM est conforme (seule condition pour signer sans produire de dkim=fail). Un DNS injoignable ne conclut
// rien : le statut précédent est gardé (une panne DNS ne coupe pas la signature d'un domaine vérifié, elle n'en vérifie pas un neuf).
func StatusOf(prev string, checks []Check) string {
	for _, c := range checks {
		if c.Kind != KindDKIM {
			continue
		}
		switch {
		case c.OK:
			return StatusVerified
		case c.Verdict == "temperror" && prev == StatusVerified:
			return StatusVerified
		}
	}
	return StatusPending
}
