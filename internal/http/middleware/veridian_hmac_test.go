package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHubSecret = "test-hub-secret"

func sign(t *testing.T, secret, timestamp string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func newSignedRequest(t *testing.T, secret string, body []byte) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sig := sign(t, secret, ts, body)
	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader(string(body)))
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	req.Header.Set("X-Veridian-Timestamp", ts)
	return req
}

func nextHandlerThatReadsBody(t *testing.T, called *atomic.Bool, expectedBody []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		got, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, expectedBody, got, "next handler must see same body")
		w.WriteHeader(http.StatusOK)
	})
}

func TestVeridianHMAC_ValidSignaturePassesThrough(t *testing.T) {
	body := []byte(`{"tenant_id":"ws-1","plan":"pro"}`)
	var called atomic.Bool
	mw := VeridianHMACMiddleware(testHubSecret)(nextHandlerThatReadsBody(t, &called, body))

	req := newSignedRequest(t, testHubSecret, body)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load(), "next handler must be called")
}

func TestVeridianHMAC_BodyRebufferedForNextHandler(t *testing.T) {
	body := []byte(`{"key":"value with spaces and é unicode"}`)
	var called atomic.Bool
	mw := VeridianHMACMiddleware(testHubSecret)(nextHandlerThatReadsBody(t, &called, body))

	req := newSignedRequest(t, testHubSecret, body)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
	// Verifier que ContentLength a été restitué
	assert.Equal(t, int64(len(body)), req.ContentLength)
}

func TestVeridianHMAC_EmptySecretReturns503(t *testing.T) {
	mw := VeridianHMACMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must NOT be called when secret is empty")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestVeridianHMAC_MissingHeadersReturns401(t *testing.T) {
	mw := VeridianHMACMiddleware(testHubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must NOT be called")
	}))

	cases := []struct {
		name  string
		setup func(*http.Request)
	}{
		{"no headers", func(r *http.Request) {}},
		{"missing signature", func(r *http.Request) {
			r.Header.Set("X-Veridian-Timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
		}},
		{"missing timestamp", func(r *http.Request) {
			r.Header.Set("X-Veridian-Hub-Signature", "deadbeef")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader("{}"))
			tc.setup(req)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

func TestVeridianHMAC_InvalidTimestampReturns401(t *testing.T) {
	mw := VeridianHMACMiddleware(testHubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must NOT be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader("{}"))
	req.Header.Set("X-Veridian-Hub-Signature", "deadbeef")
	req.Header.Set("X-Veridian-Timestamp", "not-a-number")

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianHMAC_ExpiredTimestampReturns401(t *testing.T) {
	mw := VeridianHMACMiddleware(testHubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must NOT be called")
	}))

	body := []byte("{}")
	// Timestamp 10 minutes dans le passé (drift max = 5 min)
	expiredTs := strconv.FormatInt(time.Now().Add(-10*time.Minute).UnixMilli(), 10)
	sig := sign(t, testHubSecret, expiredTs, body)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader(string(body)))
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	req.Header.Set("X-Veridian-Timestamp", expiredTs)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Aussi : timestamp futur >5 min
	futureTs := strconv.FormatInt(time.Now().Add(10*time.Minute).UnixMilli(), 10)
	sig2 := sign(t, testHubSecret, futureTs, body)
	req2 := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader(string(body)))
	req2.Header.Set("X-Veridian-Hub-Signature", sig2)
	req2.Header.Set("X-Veridian-Timestamp", futureTs)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestVeridianHMAC_InvalidSignatureReturns401(t *testing.T) {
	mw := VeridianHMACMiddleware(testHubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must NOT be called")
	}))

	body := []byte(`{"tenant_id":"x"}`)
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	// signature avec un mauvais secret
	wrongSig := sign(t, "wrong-secret", ts, body)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader(string(body)))
	req.Header.Set("X-Veridian-Hub-Signature", wrongSig)
	req.Header.Set("X-Veridian-Timestamp", ts)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Signature mal formee (pas du hex)
	req2 := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader(string(body)))
	req2.Header.Set("X-Veridian-Hub-Signature", "ZZZ-not-hex")
	req2.Header.Set("X-Veridian-Timestamp", ts)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestVeridianHMAC_BodyTooLargeReturns413(t *testing.T) {
	mw := VeridianHMACMiddleware(testHubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must NOT be called")
	}))

	// Body de MaxBodySize + 1 octets, signature techniquement valide pour ce body
	bigBody := make([]byte, MaxBodySize+1)
	for i := range bigBody {
		bigBody[i] = 'a'
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sig := sign(t, testHubSecret, ts, bigBody)

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", strings.NewReader(string(bigBody)))
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	req.Header.Set("X-Veridian-Timestamp", ts)

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

// VerifyVeridianHubSignature est la seule définition d'une signature valide (middleware et exemption du rate-limit).
func TestVerifyVeridianHubSignature(t *testing.T) {
	body := []byte(`{"tenant_id":"t"}`)
	req := newSignedRequest(t, testHubSecret, body)
	status, msg := VerifyVeridianHubSignature(req, testHubSecret)
	require.Equal(t, 0, status, msg)
	got, _ := io.ReadAll(req.Body)
	assert.Equal(t, body, got, "le corps est rendu après vérification")

	bad := newSignedRequest(t, testHubSecret, body)
	bad.Header.Set("X-Veridian-Hub-Signature", "deadbeef")
	status, _ = VerifyVeridianHubSignature(bad, testHubSecret)
	assert.Equal(t, http.StatusUnauthorized, status)
	got, _ = io.ReadAll(bad.Body)
	assert.Equal(t, body, got, "même refusée, la requête garde son corps (l'exemption du rate-limit ne la consomme pas)")

	status, _ = VerifyVeridianHubSignature(newSignedRequest(t, testHubSecret, body), "")
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

func signedV2(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	SignVeridianHubRequest(req, testHubSecret, body)
	return req
}

func runHMAC(req *http.Request) (int, bool) {
	reached := false
	h := VeridianHMACMiddleware(testHubSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, reached
}

// v2 (26/11) : la signature lie la méthode, le chemin et la query, et ne sert qu'une fois.
func TestVeridianHMAC_V2_NonRejouableSurUneAutreRoute(t *testing.T) {
	body := []byte(`{"reason":"essai"}`)
	ok := signedV2(t, http.MethodPost, "/api/tenants/a/soft-delete", body)
	code, reached := runHMAC(ok)
	require.Equal(t, 200, code)
	require.True(t, reached)

	for name, mutate := range map[string]func(*http.Request){
		"autre tenant":  func(r *http.Request) { r.URL.Path = "/api/tenants/b/soft-delete" },
		"autre route":   func(r *http.Request) { r.URL.Path = "/api/tenants/a/purge" },
		"autre méthode": func(r *http.Request) { r.Method = http.MethodDelete },
		"autre query":   func(r *http.Request) { r.URL.RawQuery = "email=victime@x.test" },
	} {
		req := signedV2(t, http.MethodPost, "/api/tenants/a/soft-delete", body)
		mutate(req)
		code, reached := runHMAC(req)
		assert.Equal(t, http.StatusUnauthorized, code, name)
		assert.False(t, reached, name)
	}

	replay := signedV2(t, http.MethodGet, "/api/hub/users?limit=5", nil)
	sig, ts := replay.Header.Get("X-Veridian-Hub-Signature"), replay.Header.Get("X-Veridian-Timestamp")
	code, _ = runHMAC(replay)
	require.Equal(t, 200, code)
	again := httptest.NewRequest(http.MethodGet, "/api/hub/users?limit=5", nil)
	again.Header.Set("X-Veridian-Hub-Signature", sig)
	again.Header.Set("X-Veridian-Timestamp", ts)
	again.Header.Set(VeridianSigVersionHeader, "2")
	code, reached = runHMAC(again)
	assert.Equal(t, http.StatusUnauthorized, code, "rejeu à l'identique")
	assert.False(t, reached)
}

// La v1 reste acceptée tant que VERIDIAN_HMAC_REQUIRE_V2 n'est pas posée ; posée, elle est refusée.
func TestVeridianHMAC_V1RefuseeQuandV2Exigee(t *testing.T) {
	body := []byte(`{"x":1}`)
	code, _ := runHMAC(newSignedRequest(t, testHubSecret, body))
	require.Equal(t, 200, code)
	t.Setenv("VERIDIAN_HMAC_REQUIRE_V2", "true")
	code, reached := runHMAC(newSignedRequest(t, testHubSecret, body))
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.False(t, reached)
	code, _ = runHMAC(signedV2(t, http.MethodPost, "/api/tenants/a/status", body))
	assert.Equal(t, 200, code)
}

func TestSignVeridianHubRequest_PoseLesTroisEnTetes(t *testing.T) {
	req := signedV2(t, http.MethodPost, "/api/x", []byte("{}"))
	assert.Equal(t, "2", req.Header.Get(VeridianSigVersionHeader))
	assert.NotEmpty(t, req.Header.Get("X-Veridian-Timestamp"))
	assert.Len(t, req.Header.Get("X-Veridian-Hub-Signature"), 64)
}

func TestSeenSignatures_PurgeEtSaturation(t *testing.T) {
	s := &seenSignatures{seen: map[string]time.Time{}}
	now := time.Now()
	assert.True(t, s.firstUse("a", now))
	assert.False(t, s.firstUse("a", now))
	for i := 0; i < veridianSeenSignaturesMax; i++ {
		s.seen[strconv.Itoa(i)] = now.Add(-time.Hour) // vieilles : purgées au prochain passage
	}
	assert.True(t, s.firstUse("b", now), "les signatures hors fenêtre sont oubliées")
	assert.LessOrEqual(t, len(s.seen), 2)
}
