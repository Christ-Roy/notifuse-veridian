package domauth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleMsg = "From: Client <bonjour@client.fr>\r\nTo: prospect@exemple.test\r\nSubject: Essai\r\nDate: Thu, 26 Nov 2026 10:00:00 +0100\r\n" +
	"Message-ID: <abc@client.fr>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nBonjour,\r\nceci est un essai.\r\n"

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, pub, err := GenerateKey()
	require.NoError(t, err)
	k, err := ParsePrivateKey(priv)
	require.NoError(t, err)
	return k, pub
}

// verifyWith vérifie toutes les signatures du message avec go-msgauth, le DNS ne connaissant que notre clé publique.
func verifyWith(t *testing.T, raw []byte, name, pub string) []*dkim.Verification {
	t.Helper()
	v, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{LookupTXT: func(n string) ([]string, error) {
		if n == name {
			return []string{"v=DKIM1; k=rsa; p=" + pub}, nil
		}
		return nil, errors.New("inconnu : " + n)
	}})
	require.NoError(t, err)
	return v
}

func TestSignMessage_VerifieParGoMsgauth(t *testing.T) {
	k, pub := testKey(t)
	out, err := SignMessage([]byte(sampleMsg), "client.fr", "vd202611abcdef", k)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(out), "DKIM-Signature:"))
	v := verifyWith(t, out, "vd202611abcdef._domainkey.client.fr", pub)
	require.Len(t, v, 1)
	assert.NoError(t, v[0].Err)
	assert.Equal(t, "client.fr", v[0].Domain)
	for _, h := range []string{"from", "to", "subject", "date", "message-id", "content-type"} {
		assert.Contains(t, lower(v[0].HeaderKeys), h)
	}

	// Corps modifié en route : la signature tombe.
	bad := bytes.Replace(out, []byte("ceci est un essai"), []byte("ceci est un faux"), 1)
	v = verifyWith(t, bad, "vd202611abcdef._domainkey.client.fr", pub)
	assert.Error(t, v[0].Err)

	// Un second From ajouté en route : la sur-signature fait tomber la signature.
	added := bytes.Replace(out, []byte("From: Client"), []byte("From: pirate@ailleurs.test\r\nFrom: Client"), 1)
	v = verifyWith(t, added, "vd202611abcdef._domainkey.client.fr", pub)
	assert.Error(t, v[0].Err)

	// Canonicalisation souple : un relais qui replie un en-tête ne casse rien.
	folded := bytes.Replace(out, []byte("Subject: Essai"), []byte("Subject:  Essai "), 1)
	v = verifyWith(t, folded, "vd202611abcdef._domainkey.client.fr", pub)
	assert.NoError(t, v[0].Err)
}

func lower(l []string) []string {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = strings.ToLower(s)
	}
	return out
}

func TestFromDomain(t *testing.T) {
	assert.Equal(t, "client.fr", FromDomain("Bonjour@Client.FR"))
	assert.Equal(t, "client.fr", FromDomain("Client <bonjour@client.fr>"))
	assert.Equal(t, "", FromDomain("sans-arobase"))
}

type keys struct {
	sel   string
	key   *rsa.PrivateKey
	err   error
	calls int
	asked []string
}

func (k *keys) SigningKey(_ context.Context, ws, domain string) (*SigningKey, error) {
	k.calls++
	k.asked = append(k.asked, ws+"|"+domain)
	if k.err != nil || k.key == nil {
		return nil, k.err
	}
	return &SigningKey{ID: "d1", Selector: k.sel, Key: k.key, LastChecked: time.Now()}, nil
}

func TestSigner_SigneSeulementUnDomaineVerifie(t *testing.T) {
	k, pub := testKey(t)
	src := &keys{sel: "vd202611abcdef", key: k}
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	s := &Signer{Keys: src, Now: func() time.Time { return now }}

	out, ok := s.Sign(context.Background(), "wsA", "bonjour@Client.fr", []byte(sampleMsg))
	require.True(t, ok)
	assert.NoError(t, verifyWith(t, out, "vd202611abcdef._domainkey.client.fr", pub)[0].Err)
	assert.Equal(t, []string{"wsA|client.fr"}, src.asked, "la clé se cherche par espace ET domaine du From")

	// Cache : pas de seconde requête dans la minute ; Forget la force.
	_, _ = s.Sign(context.Background(), "wsA", "bonjour@client.fr", []byte(sampleMsg))
	assert.Equal(t, 1, src.calls)
	s.Forget("wsA", "client.fr")
	_, _ = s.Sign(context.Background(), "wsA", "bonjour@client.fr", []byte(sampleMsg))
	assert.Equal(t, 2, src.calls)
	now = now.Add(2 * time.Minute)
	_, _ = s.Sign(context.Background(), "wsA", "bonjour@client.fr", []byte(sampleMsg))
	assert.Equal(t, 3, src.calls)

	// Domaine non vérifié (aucune clé rendue) : le message part tel quel.
	none := &Signer{Keys: &keys{}}
	_, ok = none.Sign(context.Background(), "wsA", "bonjour@client.fr", []byte(sampleMsg))
	assert.False(t, ok)
	_, ok = s.Sign(context.Background(), "", "bonjour@client.fr", []byte(sampleMsg))
	assert.False(t, ok, "sans espace, jamais de signature")

	// Clé illisible : journalisé, message intact.
	var logged string
	broken := &Signer{Keys: &keys{err: errors.New("SECRET_KEY")}, OnError: func(ws, d string, err error) { logged = ws + " " + d + " " + err.Error() }}
	_, ok = broken.Sign(context.Background(), "wsA", "bonjour@client.fr", []byte(sampleMsg))
	assert.False(t, ok)
	assert.Equal(t, "wsA client.fr SECRET_KEY", logged)
}

func TestSigner_ClePubliqueCoherenteAvecLeTXT(t *testing.T) {
	k, pub := testKey(t)
	der, err := base64.StdEncoding.DecodeString(pub)
	require.NoError(t, err)
	p, err := x509.ParsePKIXPublicKey(der)
	require.NoError(t, err)
	assert.True(t, k.PublicKey.Equal(p), "le p= publié doit être la moitié publique de la clé qui signe")
}
