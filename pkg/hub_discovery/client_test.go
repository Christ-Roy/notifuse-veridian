package hub_discovery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── encodeURIComponentGo : conformite encodeURIComponent JS ────────────────

func TestEncodeURIComponentGo_UnreservedChars(t *testing.T) {
	// Pas d'encodage pour les chars unreserved RFC 3986 + ! ~ * ' ( )
	unreserved := "ABCabc012-_.!~*'()"
	assert.Equal(t, unreserved, encodeURIComponentGo(unreserved))
}

func TestEncodeURIComponentGo_Space(t *testing.T) {
	// encodeURIComponent(" ") -> "%20"  (PAS "+" comme url.QueryEscape)
	assert.Equal(t, "%20", encodeURIComponentGo(" "))
}

func TestEncodeURIComponentGo_Plus(t *testing.T) {
	// encodeURIComponent("+") -> "%2B" (idem url.QueryEscape, OK pour emails)
	assert.Equal(t, "%2B", encodeURIComponentGo("+"))
}

func TestEncodeURIComponentGo_At(t *testing.T) {
	// encodeURIComponent("@") -> "%40"
	assert.Equal(t, "%40", encodeURIComponentGo("@"))
}

func TestEncodeURIComponentGo_EmailRealistic(t *testing.T) {
	// Email avec alias + : "alice+test@example.com"
	expected := "alice%2Btest%40example.com"
	assert.Equal(t, expected, encodeURIComponentGo("alice+test@example.com"))
}

// ─── encodeSortedQuery : tri alphabetique + format ──────────────────────────

func TestEncodeSortedQuery_SingleParam(t *testing.T) {
	q := url.Values{}
	q.Set("email", "alice@example.com")
	assert.Equal(t, "email=alice%40example.com", encodeSortedQuery(q))
}

func TestEncodeSortedQuery_MultipleParams_SortedAlpha(t *testing.T) {
	// Ordre d'insertion : "z" puis "a" -> output doit etre "a=...&z=..."
	q := url.Values{}
	q.Set("z", "1")
	q.Set("a", "2")
	got := encodeSortedQuery(q)
	assert.Equal(t, "a=2&z=1", got)
}

func TestEncodeSortedQuery_EmptyParams(t *testing.T) {
	q := url.Values{}
	assert.Equal(t, "", encodeSortedQuery(q))
}

// ─── buildCanonicalGetString : format aligne sur Hub TypeScript ─────────────

func TestBuildCanonicalGetString_BasicEmailQuery(t *testing.T) {
	q := url.Values{}
	q.Set("email", "alice@example.com")
	got := buildCanonicalGetString("GET", "/api/users/by-email", q, "1700000000000")
	expected := "1700000000000.GET./api/users/by-email?email=alice%40example.com"
	assert.Equal(t, expected, got)
}

func TestBuildCanonicalGetString_MethodUppercased(t *testing.T) {
	q := url.Values{}
	q.Set("email", "a@b.c")
	// Meme si on passe "get" minuscule, output doit etre "GET"
	got := buildCanonicalGetString("get", "/api/users/by-email", q, "1")
	assert.Contains(t, got, ".GET.")
}

func TestBuildCanonicalGetString_NoQuery_OmitsQuestionMark(t *testing.T) {
	got := buildCanonicalGetString("GET", "/healthz", url.Values{}, "1")
	assert.Equal(t, "1.GET./healthz", got)
}

// ─── NewClient : config / mode disabled ─────────────────────────────────────

func TestNewClient_EmptySecret_ReturnsDisabledClient(t *testing.T) {
	c := NewClient(Config{Secret: ""})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	assert.NoError(t, err)
}

func TestNewClient_WhitespaceSecret_ReturnsDisabledClient(t *testing.T) {
	c := NewClient(Config{Secret: "   "})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	assert.NoError(t, err)
}

func TestNewClient_DefaultBaseURL(t *testing.T) {
	c := NewClient(Config{Secret: "s"}).(*httpClient)
	assert.Equal(t, DefaultHubBaseURL, c.baseURL)
}

func TestNewClient_BaseURLTrailingSlashStripped(t *testing.T) {
	c := NewClient(Config{Secret: "s", BaseURL: "https://hub.example.com/"}).(*httpClient)
	assert.Equal(t, "https://hub.example.com", c.baseURL)
}

func TestNewClient_DefaultTimeout(t *testing.T) {
	c := NewClient(Config{Secret: "s"}).(*httpClient)
	assert.Equal(t, DefaultTimeout, c.timeout)
}

// ─── LookupByEmail : happy path + integration via httptest ──────────────────

func TestLookupByEmail_HappyPath_ExistsTrue(t *testing.T) {
	secret := "test-secret-1234"
	receivedHeaders := http.Header{}
	receivedURL := ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		receivedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"exists":true,"tenants":[{"app":"notifuse","role":"owner"},{"app":"prospection","role":"member"}]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: secret, BaseURL: srv.URL, Timeout: 2 * time.Second})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	assert.True(t, exists)
	require.Len(t, tenants, 2)
	assert.Equal(t, "notifuse", tenants[0].App)
	assert.Equal(t, "owner", tenants[0].Role)
	assert.Equal(t, "prospection", tenants[1].App)
	assert.Equal(t, "member", tenants[1].Role)

	// Headers attendus
	assert.Equal(t, CallerApp, receivedHeaders.Get(AppHeaderName))
	assert.NotEmpty(t, receivedHeaders.Get(TimestampHeaderName))
	assert.NotEmpty(t, receivedHeaders.Get(SignatureHeaderName))

	// URL doit contenir email encode (avec %40, pas +)
	assert.Contains(t, receivedURL, "email=alice%40example.com")
}

func TestLookupByEmail_HappyPath_ExistsFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"exists":false,"tenants":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})
	exists, tenants, err := c.LookupByEmail(context.Background(), "ghost@example.com")
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Empty(t, tenants)
}

func TestLookupByEmail_VerifyHMACMatchesHubSpec(t *testing.T) {
	// On verifie cote serveur que la signature reconstituee avec le meme
	// algo (timestamp + GET + path + sorted query) matche celle envoyee.
	secret := "abc123"
	var verified bool
	var verifiedReason string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts := r.Header.Get(TimestampHeaderName)
		sig := r.Header.Get(SignatureHeaderName)
		// Reconstruire la canonical string cote serveur
		canonical := ts + ".GET." + r.URL.Path + "?" + r.URL.RawQuery
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(sig), []byte(expected)) {
			verified = true
		} else {
			verifiedReason = "sig=" + sig + " expected=" + expected + " canonical=" + canonical
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"exists":true,"tenants":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: secret, BaseURL: srv.URL})
	_, _, err := c.LookupByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	assert.True(t, verified, "HMAC signature mismatch: %s", verifiedReason)
}

func TestLookupByEmail_BestEffortOnTimeout(t *testing.T) {
	// Server qui ne repond jamais dans le delai imparti
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	// Best-effort : timeout -> (false, nil, nil)
	assert.False(t, exists)
	assert.Nil(t, tenants)
	assert.NoError(t, err)
}

func TestLookupByEmail_BestEffortOn5xx_ReturnsErrorForObservability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"hub down"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	// 503 -> error remonte (signal observability) MAIS exists=false, tenants=nil
	assert.False(t, exists)
	assert.Nil(t, tenants)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestLookupByEmail_BestEffortOn401_ReturnsErrorForObservability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "wrong", BaseURL: srv.URL})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestLookupByEmail_BestEffortOnRateLimit429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestLookupByEmail_BestEffortOnMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{this is not json`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	// 200 + JSON casse -> on retourne (false, nil, nil) silencieux
	assert.False(t, exists)
	assert.Nil(t, tenants)
	assert.NoError(t, err)
}

func TestLookupByEmail_EmptyEmail_ReturnsError(t *testing.T) {
	c := NewClient(Config{Secret: "s", BaseURL: "https://hub.example.com"})
	exists, tenants, err := c.LookupByEmail(context.Background(), "")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty email")
}

func TestLookupByEmail_WhitespaceEmail_Trimmed(t *testing.T) {
	var receivedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"exists":false,"tenants":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})
	_, _, err := c.LookupByEmail(context.Background(), "  alice@example.com  ")
	require.NoError(t, err)
	assert.Contains(t, receivedURL, "alice%40example.com")
	assert.NotContains(t, receivedURL, "%20") // pas d'espace dans la query
}

// ─── Connection refused / DNS error ─────────────────────────────────────────

// fakeHTTPErrClient simule une erreur de transport (connection refused, DNS).
type fakeHTTPErrClient struct {
	err error
}

func (f *fakeHTTPErrClient) Do(req *http.Request) (*http.Response, error) {
	return nil, f.err
}

func TestLookupByEmail_BestEffortOnConnectionRefused(t *testing.T) {
	c := NewClient(Config{
		Secret:     "s",
		BaseURL:    "https://hub.example.com",
		HTTPClient: &fakeHTTPErrClient{err: errors.New("connection refused")},
	})
	exists, tenants, err := c.LookupByEmail(context.Background(), "alice@example.com")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	assert.NoError(t, err)
}

func TestLookupByEmail_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // annule immediatement

	exists, tenants, err := c.LookupByEmail(ctx, "alice@example.com")
	assert.False(t, exists)
	assert.Nil(t, tenants)
	// ctx cancelled -> best-effort silent
	assert.NoError(t, err)
}

// ─── Test full URL path (regression check) ──────────────────────────────────

func TestLookupByEmail_PathContainsByEmailEndpoint(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"exists":false,"tenants":[]}`))
	}))
	defer srv.Close()

	c := NewClient(Config{Secret: "s", BaseURL: srv.URL})
	_, _, err := c.LookupByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, DiscoveryByEmailPath, receivedPath)
	assert.True(t, strings.HasPrefix(receivedPath, "/api/"))
}
