package domauth

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeDomain(t *testing.T) {
	ok := map[string]string{"Client.FR": "client.fr", " mail.client.fr. ": "mail.client.fr", "café.fr": "xn--caf-dma.fr"}
	for in, want := range ok {
		got, err := NormalizeDomain(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "fr", "co.uk", "a@b.fr", "-a.fr", "a..fr", "http://a.fr", "a b.fr", "a_b.fr", strings.Repeat("a", 64) + ".fr"} {
		_, err := NormalizeDomain(bad)
		assert.ErrorIs(t, err, ErrInvalidName, bad)
	}
}

func TestGenerateKey_RSA2048RoundTrip(t *testing.T) {
	priv, pub, err := GenerateKey()
	require.NoError(t, err)
	k, err := ParsePrivateKey(priv)
	require.NoError(t, err)
	assert.Equal(t, 2048, k.N.BitLen())
	assert.NotContains(t, pub, "PRIVATE")
	_, err = ParsePrivateKey("n'importe quoi")
	assert.Error(t, err)
}

func TestNewSelector_DateEtUnique(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	a, err := NewSelector(now)
	require.NoError(t, err)
	b, _ := NewSelector(now)
	assert.True(t, strings.HasPrefix(a, "vd202611"))
	assert.Len(t, a, 14)
	assert.NotEqual(t, a, b)
}

func TestRecords_ValeursExactes(t *testing.T) {
	d := Domain{Domain: "client.fr", Selector: "vd202611abcdef", PublicKey: "MIIBCgKC"}
	r := Records(d, "_spf-envoi.veridian.site")
	require.Len(t, r, 3)
	assert.Equal(t, Record{Kind: KindDKIM, Type: "TXT", Name: "vd202611abcdef._domainkey.client.fr", Value: "v=DKIM1; k=rsa; p=MIIBCgKC", Required: true, Note: r[0].Note}, r[0])
	assert.Equal(t, "client.fr", r[1].Name)
	assert.Equal(t, "v=spf1 include:_spf-envoi.veridian.site ~all", r[1].Value)
	assert.True(t, r[1].Required)
	assert.Equal(t, "_dmarc.client.fr", r[2].Name)
	assert.Equal(t, "v=DMARC1; p=none; adkim=r; aspf=r", r[2].Value)
	assert.False(t, r[2].Required)
}

func TestDomain_Complete(t *testing.T) {
	d := Domain{Checks: []Check{{Kind: KindDKIM, OK: true}, {Kind: KindSPF, OK: true}, {Kind: KindDMARC, OK: false}}}
	assert.False(t, d.Complete())
	d.Checks[2].OK = true
	assert.True(t, d.Complete())
}
