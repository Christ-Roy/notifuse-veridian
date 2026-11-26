package domauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const inc = "_spf-envoi.veridian.site"

func fixtureDomain() Domain {
	return Domain{Domain: "client.fr", Selector: "vd202611abcdef", PublicKey: "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAtest"}
}

// Un domaine bien publié : DKIM avec notre clé (coupée en deux chaînes, comme le fait un DNS), SPF par notre include, DMARC.
func goodDNS(d Domain) *fakeDNS {
	f := newDNS()
	f.txt[DKIMName(d)] = []string{"v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOC AQ8AMIIBCgKCAQEAtest"}
	f.txt[d.Domain] = []string{"google-site-verification=xyz", "v=spf1 include:" + inc + " -all"}
	f.txt[inc] = []string{"v=spf1 ip4:37.187.199.185 ip4:51.210.7.44 ~all"}
	f.txt["_dmarc."+d.Domain] = []string{"v=DMARC1; p=none; adkim=r; aspf=r"}
	return f
}

func byKind(cs []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range cs {
		m[c.Kind] = c
	}
	return m
}

func TestVerify_ToutPublie(t *testing.T) {
	d := fixtureDomain()
	c := byKind((&Verifier{Resolver: goodDNS(d), Include: inc}).Verify(context.Background(), d))
	for _, k := range []string{KindDKIM, KindSPF, KindDMARC} {
		assert.True(t, c[k].OK, "%s : %+v", k, c[k])
		assert.Equal(t, "pass", c[k].Verdict, k)
	}
	assert.Equal(t, StatusVerified, StatusOf(StatusPending, []Check{c[KindDKIM]}))
}

func TestVerify_DKIM(t *testing.T) {
	d := fixtureDomain()
	cases := map[string]func(f *fakeDNS){
		"missing":   func(f *fakeDNS) { delete(f.txt, DKIMName(d)) },
		"mismatch":  func(f *fakeDNS) { f.txt[DKIMName(d)] = []string{"v=DKIM1; k=rsa; p=AUTRECLE"} },
		"temperror": func(f *fakeDNS) { f.fail[DKIMName(d)] = true },
	}
	for verdict, mut := range cases {
		f := goodDNS(d)
		mut(f)
		c := byKind((&Verifier{Resolver: f, Include: inc}).Verify(context.Background(), d))[KindDKIM]
		assert.False(t, c.OK, verdict)
		assert.Equal(t, verdict, c.Verdict)
		assert.Equal(t, StatusPending, StatusOf(StatusPending, []Check{c}), verdict)
	}
}

func TestStatusOf_PanneDNSGardeLeVerifie(t *testing.T) {
	temp := []Check{{Kind: KindDKIM, Verdict: "temperror"}}
	assert.Equal(t, StatusVerified, StatusOf(StatusVerified, temp))
	assert.Equal(t, StatusPending, StatusOf(StatusPending, temp))
	// Une clé retirée du DNS (missing) fait tomber le statut : on ne signe plus avec une clé que personne ne peut vérifier.
	assert.Equal(t, StatusPending, StatusOf(StatusVerified, []Check{{Kind: KindDKIM, Verdict: "missing"}}))
}

func TestVerify_SPF(t *testing.T) {
	d := fixtureDomain()
	cases := []struct {
		name    string
		mut     func(f *fakeDNS)
		ok      bool
		verdict string
	}{
		{"absent", func(f *fakeDNS) { f.txt[d.Domain] = []string{"autre"} }, false, "missing"},
		{"deux SPF", func(f *fakeDNS) { f.txt[d.Domain] = append(f.txt[d.Domain], "v=spf1 -all") }, false, "multiple"},
		{"sans nos relais", func(f *fakeDNS) { f.txt[d.Domain] = []string{"v=spf1 include:_spf.google.com -all"} }, false, "mismatch"},
		{"nos IP recopiées sans include", func(f *fakeDNS) { f.txt[d.Domain] = []string{"v=spf1 ip4:37.187.199.185 ip4:51.210.7.44 -all"} }, true, "pass"},
		{"un seul relais sur deux", func(f *fakeDNS) { f.txt[d.Domain] = []string{"v=spf1 ip4:37.187.199.185 -all"} }, false, "mismatch"},
		{"panne", func(f *fakeDNS) { f.fail[d.Domain] = true }, false, "temperror"},
		{"include illisible mais présent", func(f *fakeDNS) { f.fail[inc] = true }, true, "pass"},
	}
	for _, tc := range cases {
		f := goodDNS(d)
		tc.mut(f)
		c := byKind((&Verifier{Resolver: f, Include: inc}).Verify(context.Background(), d))[KindSPF]
		assert.Equal(t, tc.ok, c.OK, "%s : %+v", tc.name, c)
		assert.Equal(t, tc.verdict, c.Verdict, tc.name)
	}
}

func TestVerify_DMARC(t *testing.T) {
	d := fixtureDomain()
	f := goodDNS(d)
	delete(f.txt, "_dmarc."+d.Domain)
	c := byKind((&Verifier{Resolver: f, Include: inc}).Verify(context.Background(), d))[KindDMARC]
	assert.False(t, c.OK)
	assert.Equal(t, "missing", c.Verdict)

	// Sous-domaine : la politique du domaine organisationnel s'applique (RFC 7489 6.6.3).
	sub := d
	sub.Domain = "envoi.client.fr"
	f = goodDNS(sub)
	delete(f.txt, "_dmarc."+sub.Domain)
	f.txt["_dmarc.client.fr"] = []string{"v=DMARC1; p=quarantine"}
	c = byKind((&Verifier{Resolver: f, Include: inc}).Verify(context.Background(), sub))[KindDMARC]
	require.True(t, c.OK, "%+v", c)
	assert.Equal(t, "inherited", c.Verdict)
}
