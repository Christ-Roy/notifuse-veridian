package middleware

import (
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
		name string
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
