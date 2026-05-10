package domain

// === Veridian patch ===
// Tests unitaires de BuildAutoLoginURL + VerifyAutoLoginToken.
// Cible : crypto, TTL, payload integrity, all error paths.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianBuildAutoLoginURL_OK(t *testing.T) {
	url, exp, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)
	assert.Contains(t, url, "https://api.example.com/veridian/auto-login?token=")
	// Expiration doit etre TTL = 60s dans le futur (avec marge 1s pour le test)
	assert.WithinDuration(t, time.Now().Add(AutoLoginTokenTTL), exp, time.Second)
}

func TestVeridianBuildAutoLoginURL_StripsTrailingSlash(t *testing.T) {
	url, _, err := BuildAutoLoginURL("https://api.example.com/", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)
	// Pas de double slash
	assert.NotContains(t, url, "https://api.example.com//veridian")
	assert.Contains(t, url, "https://api.example.com/veridian/auto-login?token=")
}

func TestVeridianBuildAutoLoginURL_EmptySecretRejects(t *testing.T) {
	_, _, err := BuildAutoLoginURL("https://api.example.com", "", "ws-1", "u@x.test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HUB_API_SECRET not configured")
}

func TestVeridianBuildAutoLoginURL_TokenIsParseable(t *testing.T) {
	url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)

	// Extraire le token de l'URL
	idx := strings.Index(url, "token=")
	require.NotEqual(t, -1, idx)
	token := url[idx+len("token="):]

	// Doit avoir un point separateur
	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)

	// Premier segment = base64 url-safe valide d'un JSON AutoLoginPayload
	rawJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	var p AutoLoginPayload
	require.NoError(t, json.Unmarshal(rawJSON, &p))
	assert.Equal(t, "ws-1", p.WorkspaceID)
	assert.Equal(t, "u@x.test", p.Email)

	// Deuxieme segment = hex valide
	_, err = hex.DecodeString(parts[1])
	assert.NoError(t, err)
}

func TestVeridianVerifyAutoLoginToken_RoundTrip(t *testing.T) {
	url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-roundtrip", "owner@rt.test")
	require.NoError(t, err)

	token := url[strings.Index(url, "token=")+len("token="):]
	payload, err := VerifyAutoLoginToken(token, "secret-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-roundtrip", payload.WorkspaceID)
	assert.Equal(t, "owner@rt.test", payload.Email)
	assert.Greater(t, payload.IssuedAt, int64(0))
	assert.Greater(t, payload.ExpiresAt, payload.IssuedAt)
}

func TestVeridianVerifyAutoLoginToken_EmptyToken(t *testing.T) {
	_, err := VerifyAutoLoginToken("", "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token required")
}

func TestVeridianVerifyAutoLoginToken_MalformedToken(t *testing.T) {
	for _, malformed := range []string{
		"no-dot-separator",
		".only-trailing-dot",
		"only-leading-dot.",
	} {
		_, err := VerifyAutoLoginToken(malformed, "secret-1")
		require.Error(t, err, "expected error for token=%q", malformed)
	}
}

func TestVeridianVerifyAutoLoginToken_InvalidSignatureEncoding(t *testing.T) {
	// Premier segment valide base64, deuxieme segment invalide hex
	url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)
	token := url[strings.Index(url, "token=")+len("token="):]
	parts := strings.SplitN(token, ".", 2)

	// Remplace la signature hex par autre chose
	tampered := parts[0] + ".not-hex"
	_, err = VerifyAutoLoginToken(tampered, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature encoding")
}

func TestVeridianVerifyAutoLoginToken_TamperedSignature(t *testing.T) {
	url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)
	token := url[strings.Index(url, "token=")+len("token="):]

	// Remplace la signature par 64 hex chars zeros (taille SHA256)
	parts := strings.SplitN(token, ".", 2)
	tampered := parts[0] + "." + strings.Repeat("0", 64)
	_, err = VerifyAutoLoginToken(tampered, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestVeridianVerifyAutoLoginToken_WrongSecret(t *testing.T) {
	url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)
	token := url[strings.Index(url, "token=")+len("token="):]

	// Tente de verifier avec un autre secret
	_, err = VerifyAutoLoginToken(token, "different-secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestVeridianVerifyAutoLoginToken_TamperedPayload(t *testing.T) {
	url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-1", "u@x.test")
	require.NoError(t, err)
	token := url[strings.Index(url, "token=")+len("token="):]
	parts := strings.SplitN(token, ".", 2)

	// Forger un nouveau payload avec workspace_id different mais reutiliser
	// l'ancienne signature → doit echouer
	forged := AutoLoginPayload{
		WorkspaceID: "ws-attacker",
		Email:       "attacker@evil.test",
		IssuedAt:    time.Now().UnixMilli(),
		ExpiresAt:   time.Now().Add(time.Minute).UnixMilli(),
	}
	rawJSON, _ := json.Marshal(forged)
	forgedEncoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	tampered := forgedEncoded + "." + parts[1]

	_, err = VerifyAutoLoginToken(tampered, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestVeridianVerifyAutoLoginToken_InvalidPayloadEncoding(t *testing.T) {
	// payload en base64 invalide, signature correcte pour ce payload
	encoded := "not-valid-base64!!!"
	mac := hmac.New(sha256.New, []byte("secret-1"))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	_, err := VerifyAutoLoginToken(token, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid payload encoding")
}

func TestVeridianVerifyAutoLoginToken_InvalidPayloadJSON(t *testing.T) {
	// payload base64-valid mais JSON invalide
	encoded := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	mac := hmac.New(sha256.New, []byte("secret-1"))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	_, err := VerifyAutoLoginToken(token, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid payload JSON")
}

func TestVeridianVerifyAutoLoginToken_Expired(t *testing.T) {
	// Construire un payload avec ExpiresAt dans le passe
	expired := AutoLoginPayload{
		WorkspaceID: "ws-1",
		Email:       "u@x.test",
		IssuedAt:    time.Now().Add(-2 * time.Minute).UnixMilli(),
		ExpiresAt:   time.Now().Add(-1 * time.Minute).UnixMilli(),
	}
	rawJSON, _ := json.Marshal(expired)
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte("secret-1"))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	_, err := VerifyAutoLoginToken(token, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVeridianVerifyAutoLoginToken_IssuedInFuture(t *testing.T) {
	// Construire un payload avec IssuedAt loin dans le futur (au-dela de TTL)
	future := AutoLoginPayload{
		WorkspaceID: "ws-1",
		Email:       "u@x.test",
		IssuedAt:    time.Now().Add(10 * time.Minute).UnixMilli(),
		ExpiresAt:   time.Now().Add(15 * time.Minute).UnixMilli(),
	}
	rawJSON, _ := json.Marshal(future)
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte("secret-1"))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	_, err := VerifyAutoLoginToken(token, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "future")
}

func TestVeridianVerifyAutoLoginToken_MissingWorkspaceID(t *testing.T) {
	// Payload valide signe mais workspace_id vide
	missing := AutoLoginPayload{
		WorkspaceID: "", // vide
		Email:       "u@x.test",
		IssuedAt:    time.Now().UnixMilli(),
		ExpiresAt:   time.Now().Add(time.Minute).UnixMilli(),
	}
	rawJSON, _ := json.Marshal(missing)
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte("secret-1"))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	_, err := VerifyAutoLoginToken(token, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing workspace_id")
}

func TestVeridianVerifyAutoLoginToken_MissingEmail(t *testing.T) {
	missing := AutoLoginPayload{
		WorkspaceID: "ws-1",
		Email:       "", // vide
		IssuedAt:    time.Now().UnixMilli(),
		ExpiresAt:   time.Now().Add(time.Minute).UnixMilli(),
	}
	rawJSON, _ := json.Marshal(missing)
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte("secret-1"))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	_, err := VerifyAutoLoginToken(token, "secret-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

// Concurrence basique : Build et Verify depuis plusieurs goroutines
// (HMAC + json sont thread-safe par contrat — ce test verifie qu'on n'a pas
// introduit de mutable state accidentel).
func TestVeridianTokenBuildVerifyConcurrent(t *testing.T) {
	const N = 50
	done := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			url, _, err := BuildAutoLoginURL("https://api.example.com", "secret-1", "ws-conc", "u@conc.test")
			if err != nil {
				t.Errorf("Build failed: %v", err)
				return
			}
			token := url[strings.Index(url, "token=")+len("token="):]
			_, err = VerifyAutoLoginToken(token, "secret-1")
			if err != nil {
				t.Errorf("Verify failed: %v", err)
			}
		}(i)
	}
	for i := 0; i < N; i++ {
		<-done
	}
}
