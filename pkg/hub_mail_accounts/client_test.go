package hub_mail_accounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-1234"

// fixedNow returns a stable timestamp for deterministic HMAC verification.
func fixedNow() time.Time {
	return time.Unix(1717000000, 0).UTC()
}

func expectedSignature(secret, timestampMs string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestampMs))
	mac.Write([]byte("."))
	return hex.EncodeToString(mac.Sum(nil))
}

// ─── Disabled client ─────────────────────────────────────────────────────────

func TestNewClient_DisabledOnEmptySecret(t *testing.T) {
	c := NewClient(Config{HMACSecret: ""})
	require.NotNil(t, c)

	res, err := c.ListMailAccounts(context.Background(), "u1")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.HubAvailable)
	assert.Equal(t, []MailAccount{}, res.Accounts)

	setRes, err := c.SetDefaultAccount(context.Background(), "u1", "acc1")
	require.NoError(t, err)
	require.NotNil(t, setRes)
	assert.False(t, setRes.HubAvailable)
}

func TestNewClient_DisabledOnWhitespaceSecret(t *testing.T) {
	c := NewClient(Config{HMACSecret: "   "})
	require.NotNil(t, c)
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.False(t, res.HubAvailable)
}

// ─── ListMailAccounts ────────────────────────────────────────────────────────

func TestListMailAccounts_EmptyUserID(t *testing.T) {
	c := NewClient(Config{HMACSecret: testSecret})
	_, err := c.ListMailAccounts(context.Background(), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidParams)
}

func TestListMailAccounts_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/users/u-abc/mail-accounts", r.URL.Path)
		assert.Equal(t, "notifuse", r.Header.Get(AppHeaderName))

		ts := r.Header.Get(TimestampHeaderName)
		assert.NotEmpty(t, ts)
		assert.Equal(t, expectedSignature(testSecret, ts), r.Header.Get(SignatureHeaderName))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"accounts":[
            {"id":"acc1","provider":"google","email":"a@b.com","name":"Alice","is_default":true,"needs_reauth":false,"connected_at":"2026-05-20T10:00:00Z"},
            {"id":"acc2","provider":"microsoft","email":"x@y.com","name":"Bob","is_default":false,"needs_reauth":true,"connected_at":"2026-05-22T14:00:00Z"}
        ]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := c.ListMailAccounts(context.Background(), "u-abc")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.HubAvailable)
	require.Len(t, res.Accounts, 2)
	assert.Equal(t, "acc1", res.Accounts[0].ID)
	assert.True(t, res.Accounts[0].IsDefault)
	assert.Equal(t, "microsoft", res.Accounts[1].Provider)
	assert.True(t, res.Accounts[1].NeedsReauth)
}

func TestListMailAccounts_EmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"accounts":[]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.True(t, res.HubAvailable)
	assert.Equal(t, []MailAccount{}, res.Accounts)
}

func TestListMailAccounts_OK_NullAccountsNormalizedToEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"accounts":null}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.True(t, res.HubAvailable)
	assert.NotNil(t, res.Accounts)
	assert.Len(t, res.Accounts, 0)
}

func TestListMailAccounts_404UserNotFound_ConsideredHubAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"user_not_found"}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.True(t, res.HubAvailable, "404 user_not_found = Hub vivant, juste user inconnu")
	assert.Empty(t, res.Accounts)
}

func TestListMailAccounts_404Catchall_ConsideredHubUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<html>Not Found</html>`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.False(t, res.HubAvailable, "HTML = catchall Next.js = endpoint pas livre")
	assert.Empty(t, res.Accounts)
}

func TestListMailAccounts_500_HubUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"oops"}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.False(t, res.HubAvailable)
	assert.Empty(t, res.Accounts)
}

func TestListMailAccounts_NetworkError_HubUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // force connection refused

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.False(t, res.HubAvailable)
}

func TestListMailAccounts_InvalidJSON_HubUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<html>SPA</html>`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.ListMailAccounts(context.Background(), "u1")
	assert.False(t, res.HubAvailable, "200 avec body non-JSON = catchall SPA")
}

// ─── SetDefaultAccount ───────────────────────────────────────────────────────

func TestSetDefaultAccount_MissingParams(t *testing.T) {
	c := NewClient(Config{HMACSecret: testSecret})
	_, err := c.SetDefaultAccount(context.Background(), "", "acc1")
	assert.ErrorIs(t, err, ErrInvalidParams)
	_, err = c.SetDefaultAccount(context.Background(), "u1", "")
	assert.ErrorIs(t, err, ErrInvalidParams)
}

func TestSetDefaultAccount_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/users/u-1/mail-accounts/acc-2/default", r.URL.Path)
		ts := r.Header.Get(TimestampHeaderName)
		require.NotEmpty(t, ts)
		assert.Equal(t, expectedSignature(testSecret, ts), r.Header.Get(SignatureHeaderName))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"user_id":"u-1","account_id":"acc-2","is_default":true}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := c.SetDefaultAccount(context.Background(), "u-1", "acc-2")
	require.NoError(t, err)
	assert.True(t, res.HubAvailable)
	assert.Equal(t, "u-1", res.UserID)
	assert.Equal(t, "acc-2", res.AccountID)
	assert.True(t, res.IsDefault)
	assert.Equal(t, 200, res.HTTPStatus)
}

func TestSetDefaultAccount_404Catchall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<html>404</html>`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.SetDefaultAccount(context.Background(), "u-1", "acc-2")
	assert.False(t, res.HubAvailable, "HTML 404 = catchall = pas dispo")
	assert.Equal(t, 404, res.HTTPStatus)
}

func TestSetDefaultAccount_404AccountNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"account_not_found"}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, _ := c.SetDefaultAccount(context.Background(), "u-1", "acc-2")
	assert.True(t, res.HubAvailable)
	assert.Equal(t, "account_not_found", res.Reason)
}

// ─── HMAC canonical correctness ──────────────────────────────────────────────

func TestListMailAccounts_HMAC_SignatureFormatTsDot(t *testing.T) {
	// Verifie pixel-parfait que la signature est bien HMAC(${ts}.) — pas
	// HMAC(${ts}.GET.path?query) comme le pattern hub_discovery outbound.
	received := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts := r.Header.Get(TimestampHeaderName)
		sig := r.Header.Get(SignatureHeaderName)
		// Recompute attendu : HMAC sur "${ts}."
		expected := expectedSignature(testSecret, ts)
		received <- ts + "|" + sig + "|" + expected
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"accounts":[]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	_, err := c.ListMailAccounts(context.Background(), "u1")
	require.NoError(t, err)

	got := <-received
	parts := strings.Split(got, "|")
	require.Len(t, parts, 3)
	assert.Equal(t, parts[1], parts[2], "signature recue != HMAC(${ts}.)")
}

// ─── HTTPClient injection ────────────────────────────────────────────────────

type stubClient struct {
	resp *http.Response
	err  error
	last *http.Request
}

func (s *stubClient) Do(req *http.Request) (*http.Response, error) {
	s.last = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func TestNewClient_HonorsCustomHTTPClient(t *testing.T) {
	stub := &stubClient{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accounts":[]}`)),
		},
	}
	c := NewClient(Config{
		HubURL:     "https://hub.example",
		HMACSecret: testSecret,
		HTTPClient: stub,
	})
	res, err := c.ListMailAccounts(context.Background(), "u1")
	require.NoError(t, err)
	assert.True(t, res.HubAvailable)
	require.NotNil(t, stub.last)
	assert.Equal(t, "https://hub.example/api/users/u1/mail-accounts", stub.last.URL.String())
}

// ─── Compile-time interface contract ─────────────────────────────────────────

var _ Client = (*httpClient)(nil)
var _ Client = (*disabledClient)(nil)
