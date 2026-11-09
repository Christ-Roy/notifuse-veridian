package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIss = "https://app.veridian.site"
	testAud = "https://notifuse.app.veridian.site/api/mcp"
)

type hubKey struct {
	priv *ecdsa.PrivateKey
	kid  string
}

func newHubKey(t *testing.T, kid string) hubKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return hubKey{priv: k, kid: kid}
}

func (k hubKey) jwk() map[string]string {
	pad := func(b []byte) []byte { return append(make([]byte, 32-len(b)), b...) }
	return map[string]string{"kty": "EC", "crv": "P-256", "kid": k.kid,
		"x": base64.RawURLEncoding.EncodeToString(pad(k.priv.X.Bytes())),
		"y": base64.RawURLEncoding.EncodeToString(pad(k.priv.Y.Bytes()))}
}

func (k hubKey) sign(t *testing.T, mutate func(*Claims)) string {
	t.Helper()
	c := &Claims{Email: "u@x.test", EmailVerified: true, ClientID: "claude",
		RegisteredClaims: jwt.RegisteredClaims{Issuer: testIss, Audience: jwt.ClaimStrings{testAud}, Subject: "hub-user-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)), ID: "j1"}}
	if mutate != nil {
		mutate(c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, c)
	tok.Header["kid"] = k.kid
	s, err := tok.SignedString(k.priv)
	require.NoError(t, err)
	return s
}

// jwksServer sert le JWKS ; hits compte les telechargements.
func jwksServer(t *testing.T, keys ...hubKey) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		set := []map[string]string{}
		for _, k := range keys {
			set = append(set, k.jwk())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": set})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newVerifier(srv *httptest.Server) *Verifier {
	return &Verifier{JWKSURL: srv.URL, Issuer: testIss, Audience: testAud}
}

func TestVerifier_JetonValide(t *testing.T) {
	k := newHubKey(t, "k1")
	srv, _ := jwksServer(t, k)
	c, err := newVerifier(srv).Verify(context.Background(), k.sign(t, nil))
	require.NoError(t, err)
	assert.Equal(t, "hub-user-1", c.Subject)
	assert.Equal(t, "claude", c.ClientID)
}

func TestVerifier_Refus(t *testing.T) {
	k := newHubKey(t, "k1")
	other := newHubKey(t, "k1") // meme kid, autre cle : mauvaise signature
	srv, _ := jwksServer(t, k)
	cases := map[string]string{
		"mauvaise audience":       k.sign(t, func(c *Claims) { c.Audience = jwt.ClaimStrings{"https://autre/api/mcp"} }),
		"mauvais emetteur":        k.sign(t, func(c *Claims) { c.Issuer = "https://evil.example" }),
		"expire":                  k.sign(t, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }),
		"mauvaise signature":      other.sign(t, nil),
		"sans sub":                k.sign(t, func(c *Claims) { c.Subject = "" }),
		"email non verifie":       k.sign(t, func(c *Claims) { c.EmailVerified = false }),
		"duree de vie excessive":  k.sign(t, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(2 * time.Hour)) }),
		"pas un jeton":            "n.importe.quoi",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newVerifier(srv).Verify(context.Background(), tok)
			assert.ErrorIs(t, err, ErrUnauthorized)
		})
	}
}

func TestVerifier_RefuseHS256(t *testing.T) {
	k := newHubKey(t, "k1")
	srv, _ := jwksServer(t, k)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{EmailVerified: true,
		RegisteredClaims: jwt.RegisteredClaims{Issuer: testIss, Audience: jwt.ClaimStrings{testAud}, Subject: "s", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}})
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString([]byte("secret"))
	require.NoError(t, err)
	_, err = newVerifier(srv).Verify(context.Background(), s)
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func TestVerifier_CacheDuJWKS(t *testing.T) {
	k := newHubKey(t, "k1")
	srv, hits := jwksServer(t, k)
	v := newVerifier(srv)
	for i := 0; i < 3; i++ {
		_, err := v.Verify(context.Background(), k.sign(t, nil))
		require.NoError(t, err)
	}
	assert.Equal(t, 1, *hits, "un seul telechargement dans l'heure")

	now := time.Now().Add(61 * time.Minute)
	v.Now = func() time.Time { return now }
	_, err := v.Verify(context.Background(), k.sign(t, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(5 * time.Minute)) }))
	require.NoError(t, err)
	assert.Equal(t, 2, *hits, "recharge apres une heure")
}

func TestVerifier_RotationDeCle(t *testing.T) {
	old, fresh := newHubKey(t, "old"), newHubKey(t, "fresh")
	srv, hits := jwksServer(t, old, fresh)
	v := newVerifier(srv)
	v.keys = map[string]*ecdsa.PublicKey{} // cache present mais sans la nouvelle cle
	v.fetched = time.Now().Add(-time.Minute)
	_, err := v.Verify(context.Background(), fresh.sign(t, nil))
	require.NoError(t, err)
	assert.Equal(t, 1, *hits)
}

func TestVerifier_KidInconnuNeMartellePasLeJWKS(t *testing.T) {
	k, ghost := newHubKey(t, "k1"), newHubKey(t, "fantome")
	srv, hits := jwksServer(t, k)
	v := newVerifier(srv)
	for i := 0; i < 5; i++ {
		_, err := v.Verify(context.Background(), ghost.sign(t, nil))
		assert.ErrorIs(t, err, ErrUnauthorized)
	}
	assert.Equal(t, 1, *hits)
}

func TestParseJWKS_IgnoreLesClesInutilisables(t *testing.T) {
	k := newHubKey(t, "ok")
	raw, _ := json.Marshal(map[string]any{"keys": []any{
		map[string]string{"kty": "RSA", "kid": "rsa", "n": "x", "e": "AQAB"},
		map[string]string{"kty": "EC", "crv": "P-256", "kid": "horscourbe", "x": "AQ", "y": "AQ"},
		map[string]string{"kty": "EC", "crv": "P-256", "x": "AQ", "y": "AQ"}, // sans kid
		k.jwk(),
	}})
	keys, err := parseJWKS(raw)
	require.NoError(t, err)
	assert.Len(t, keys, 1)
	assert.Contains(t, keys, "ok")

	_, err = parseJWKS([]byte(`{"keys":[]}`))
	assert.Error(t, err)
	_, err = parseJWKS([]byte(`pas du json`))
	assert.Error(t, err)
}
