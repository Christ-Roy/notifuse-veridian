package hub_mail_gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastBackoffs remplace les delais reels par 0 pour eviter les tests
// lents (sinon TestSendMailAsUser_GivesUpAfter3Retries durerait ~14s).
// Utilisation : defer restoreBackoffs := setFastBackoffs(); defer restoreBackoffs().
func setFastBackoffs() func() {
	old := retryBackoffs
	retryBackoffs = []time.Duration{0, 0, 0}
	return func() { retryBackoffs = old }
}

const testSecret = "test-hmac-secret-32-bytes-minimum-okok"

func mustParams() SendMailParams {
	return SendMailParams{
		UserID:         "hub-user-uuid",
		To:             []string{"to@example.com"},
		Subject:        "Hello",
		BodyText:       "Plain body",
		IdempotencyKey: "11111111-1111-4111-8111-111111111111",
	}
}

// buildOKHandler renvoie un handler httptest qui repond 200 avec le
// body fourni. Capture la derniere requete dans `captured` (pointer).
type captured struct {
	body []byte
	hdrs http.Header
	hits atomic.Int32
}

func newOKServer(t *testing.T, cap *captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.hits.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		cap.body = body
		cap.hdrs = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message_id":"hub-msg-1","sent_at":"2026-05-25T12:00:00Z"}`))
	}))
}

func TestSendMailAsUser_Disabled(t *testing.T) {
	client := NewClient(Config{
		HMACSecret: "", // disabled
	})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	assert.Nil(t, res)
	assert.True(t, errors.Is(err, ErrMailGatewayDisabled))
}

func TestSendMailAsUser_InvalidParams(t *testing.T) {
	client := NewClient(Config{HubURL: "http://localhost:1", HMACSecret: testSecret})
	cases := []struct {
		name  string
		mutfn func(p *SendMailParams)
	}{
		{"empty_user_id", func(p *SendMailParams) { p.UserID = "" }},
		{"no_recipients", func(p *SendMailParams) { p.To = nil }},
		{"empty_subject", func(p *SendMailParams) { p.Subject = "  " }},
		{"no_body", func(p *SendMailParams) { p.BodyText = ""; p.BodyHTML = "" }},
		{"empty_idempotency_key", func(p *SendMailParams) { p.IdempotencyKey = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustParams()
			tc.mutfn(&p)
			res, err := client.SendMailAsUser(context.Background(), p)
			assert.Nil(t, res)
			assert.True(t, errors.Is(err, ErrInvalidParams), "expected ErrInvalidParams, got %v", err)
		})
	}
}

func TestSendMailAsUser_Success(t *testing.T) {
	cap := &captured{}
	srv := newOKServer(t, cap)
	defer srv.Close()

	client := NewClient(Config{
		HubURL:     srv.URL,
		HMACSecret: testSecret,
		Logger:     logger.NewLogger(),
	})

	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.OK)
	assert.Equal(t, "hub-msg-1", res.MessageID)
	assert.False(t, res.SentAt.IsZero())
	assert.False(t, res.IdempotentReplay)
	assert.Equal(t, 200, res.HTTPStatus)
	assert.Equal(t, int32(1), cap.hits.Load(), "expected exactly 1 HTTP call")
}

func TestSendMailAsUser_HMACSignatureCorrect(t *testing.T) {
	cap := &captured{}
	srv := newOKServer(t, cap)
	defer srv.Close()

	client := NewClient(Config{
		HubURL:     srv.URL,
		HMACSecret: testSecret,
	})

	_, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)

	sig := cap.hdrs.Get(SignatureHeaderName)
	ts := cap.hdrs.Get(TimestampHeaderName)
	app := cap.hdrs.Get(AppHeaderName)
	ct := cap.hdrs.Get("Content-Type")

	require.NotEmpty(t, sig, "signature header must be set")
	require.NotEmpty(t, ts, "timestamp header must be set")
	assert.Equal(t, CallerApp, app)
	assert.Equal(t, "application/json", ct)

	// Recalcul canonical EXACTE : ${ts}.${rawBody}
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(cap.body)
	expected := hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, expected, sig, "signature HMAC must match canonical ${ts}.${rawBody}")

	// Drift check
	tsMs, err := strconv.ParseInt(ts, 10, 64)
	require.NoError(t, err)
	drift := time.Since(time.UnixMilli(tsMs))
	assert.True(t, drift < 5*time.Second && drift > -5*time.Second, "ts drift too large: %v", drift)
}

func TestSendMailAsUser_ContractVersionSet(t *testing.T) {
	cap := &captured{}
	srv := newOKServer(t, cap)
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	_, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(cap.body, &got))
	assert.Equal(t, "1.0", got["contract_version"])
	assert.Equal(t, "hub-user-uuid", got["user_id"])
	// `to` doit etre marshalled comme array (jamais comme string)
	toArr, ok := got["to"].([]interface{})
	require.True(t, ok, "to must be marshalled as []interface{}")
	assert.Equal(t, []interface{}{"to@example.com"}, toArr)
	assert.Equal(t, "Plain body", got["body_text"])
	// body_html absent (omitempty)
	_, hasHTML := got["body_html"]
	assert.False(t, hasHTML)
}

func TestSendMailAsUser_UserNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"user_not_found"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonUserNotFound, res.Reason)
	assert.Equal(t, 404, res.HTTPStatus)
}

func TestSendMailAsUser_NeedsReauth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":"needs_reauth"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonNeedsReauth, res.Reason)
	assert.Equal(t, 412, res.HTTPStatus)
}

func TestSendMailAsUser_ProviderNotLinked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"provider_not_linked"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonProviderNotLinked, res.Reason)
	assert.Equal(t, 422, res.HTTPStatus)
}

func TestSendMailAsUser_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limit"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonRateLimit, res.Reason)
	assert.Equal(t, 429, res.HTTPStatus)
}

func TestSendMailAsUser_InvalidPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_payload","details":"to[0] not email"}`))
	}))
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonInvalidPayload, res.Reason)
	assert.Equal(t, 400, res.HTTPStatus)
}

func TestSendMailAsUser_InvalidHMAC_NoRetry(t *testing.T) {
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_hmac"}`))
	}))
	defer srv.Close()

	defer setFastBackoffs()()
	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonInvalidHMAC, res.Reason)
	assert.Equal(t, 401, res.HTTPStatus)
	// 4xx -> pas de retry
	assert.Equal(t, int32(1), hits.Load(), "401 must NOT trigger retry")
}

func TestSendMailAsUser_RetryOn5xx(t *testing.T) {
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"db_down"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message_id":"retry-ok","sent_at":"2026-05-25T12:00:00Z"}`))
	}))
	defer srv.Close()

	defer setFastBackoffs()()
	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.OK, "should succeed after 1 retry")
	assert.Equal(t, "retry-ok", res.MessageID)
	assert.Equal(t, int32(2), hits.Load(), "expected exactly 2 HTTP calls (1 fail + 1 retry success)")
}

func TestSendMailAsUser_GivesUpAfter3Retries(t *testing.T) {
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"provider_unreachable"}`))
	}))
	defer srv.Close()

	defer setFastBackoffs()()
	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonUnreachable, res.Reason, "after retries epuises, reason must be unreachable")
	assert.Equal(t, int32(MaxRetries), hits.Load(), "expected exactly MaxRetries HTTP calls")
}

func TestSendMailAsUser_NetworkError_NoLeak(t *testing.T) {
	// Pointer sur un port libre = connection refused
	defer setFastBackoffs()()
	client := NewClient(Config{
		HubURL:     "http://127.0.0.1:1", // port 1 = jamais ouvert
		HMACSecret: testSecret,
		HTTPClient: &http.Client{Timeout: 100 * time.Millisecond},
	})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonUnreachable, res.Reason)
	assert.Equal(t, 0, res.HTTPStatus)
}

func TestSendMailAsUser_ContextCancelDuringBackoff(t *testing.T) {
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Backoff long pour qu'on puisse cancel pendant l'attente
	old := retryBackoffs
	retryBackoffs = []time.Duration{0, 500 * time.Millisecond, 500 * time.Millisecond}
	defer func() { retryBackoffs = old }()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	res, err := client.SendMailAsUser(ctx, mustParams())
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonUnreachable, res.Reason)
	// Si on respecte ctx.Done() pendant le backoff, on sort en < 250ms
	// (pas en > 500ms qui serait l'attente du backoff complet)
	assert.True(t, elapsed < 350*time.Millisecond, "ctx cancel must short-circuit backoff (took %v)", elapsed)
}

func TestSendMailAsUser_IdempotentReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message_id":"old-msg","sent_at":"2026-05-25T11:59:00Z","idempotent_replay":true}`))
	}))
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.OK)
	assert.True(t, res.IdempotentReplay)
	assert.Equal(t, "old-msg", res.MessageID)
}

// httpClientFunc implemente HTTPClient avec une closure (alternative au
// httptest.Server quand on veut juste contrer le Do).
type httpClientFunc func(*http.Request) (*http.Response, error)

func (f httpClientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSendMailAsUser_CustomHTTPClient(t *testing.T) {
	calls := atomic.Int32{}
	mock := httpClientFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		assert.Equal(t, "POST", req.Method)
		assert.Contains(t, req.URL.Path, SendAsUserPath)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(stringReader(`{"message_id":"mock","sent_at":"2026-05-25T12:00:00Z"}`)),
			Header:     http.Header{},
		}, nil
	})

	client := NewClient(Config{
		HubURL:     "https://test.example",
		HMACSecret: testSecret,
		HTTPClient: mock,
	})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.OK)
	assert.Equal(t, "mock", res.MessageID)
	assert.Equal(t, int32(1), calls.Load())
}

// --- v1.1 — multi-comptes + rate limit per-recipient ---

// captureBody renvoie un handler httptest qui repond 200 puis capture
// le body JSON pour assertion. Variante de newOKServer qui accepte un
// response body JSON custom (utile pour tester mail_account_id_used).
func captureBody(t *testing.T, cap *captured, respBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.hits.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		cap.body = body
		cap.hdrs = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(respBody))
	}))
}

func TestSendMailAsUser_WithMailAccountID(t *testing.T) {
	cap := &captured{}
	srv := captureBody(t, cap, `{"message_id":"v11-msg","sent_at":"2026-05-25T12:00:00Z","mail_account_id_used":"acc_clx123abc"}`)
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})

	p := mustParams()
	p.MailAccountID = "acc_clx123abc"
	res, err := client.SendMailAsUser(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.OK)
	assert.Equal(t, "v11-msg", res.MessageID)
	assert.Equal(t, "acc_clx123abc", res.MailAccountIDUsed, "MailAccountIDUsed doit etre extrait de la 200 v1.1")

	// Body wire format : contract_version=1.1 + mail_account_id present
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(cap.body, &got))
	assert.Equal(t, "1.1", got["contract_version"], "MailAccountID non vide doit upgrader contract_version a 1.1")
	assert.Equal(t, "acc_clx123abc", got["mail_account_id"], "mail_account_id doit etre present dans le body")
}

func TestSendMailAsUser_WithoutMailAccountID(t *testing.T) {
	cap := &captured{}
	srv := captureBody(t, cap, `{"message_id":"v10-msg","sent_at":"2026-05-25T12:00:00Z"}`)
	defer srv.Close()

	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})

	p := mustParams() // MailAccountID vide
	res, err := client.SendMailAsUser(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.OK)
	assert.Empty(t, res.MailAccountIDUsed, "MailAccountIDUsed doit etre vide quand le Hub ne le retourne pas (v1.0)")

	// Back-compat : contract_version=1.0 + mail_account_id ABSENT du wire
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(cap.body, &got))
	assert.Equal(t, "1.0", got["contract_version"], "MailAccountID vide doit garder contract_version 1.0 (back-compat Hub v1.0)")
	_, hasField := got["mail_account_id"]
	assert.False(t, hasField, "mail_account_id NE DOIT PAS apparaitre dans le wire format quand MailAccountID est vide (omitempty)")
}

func TestSendMailAsUser_RateLimitRecipient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limit_recipient","recipient":"spam@example.com","retry_after_seconds":1200}`))
	}))
	defer srv.Close()

	defer setFastBackoffs()()
	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonRecipientRateLimited, res.Reason, "doit mapper sur le nouveau Reason v1.1, pas sur ReasonRateLimit")
	assert.Equal(t, "rate_limit_recipient", res.Reason, "valeur Reason doit miroiter exactement le champ `error` du body Hub")
	assert.Equal(t, 429, res.HTTPStatus)
	assert.Equal(t, "spam@example.com", res.Recipient, "Recipient doit etre extrait du body 429")
	assert.Equal(t, 1200, res.RetryAfterSeconds, "RetryAfterSeconds doit etre extrait du body 429")
}

func TestSendMailAsUser_RateLimitGlobal_StaysRateLimit(t *testing.T) {
	// Back-compat vague 6 : 429 sans `recipient` doit rester Reason="rate_limit"
	// (rate limit user-level global). NE PAS confondre avec rate_limit_recipient.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limit"}`))
	}))
	defer srv.Close()

	defer setFastBackoffs()()
	client := NewClient(Config{HubURL: srv.URL, HMACSecret: testSecret})
	res, err := client.SendMailAsUser(context.Background(), mustParams())
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.OK)
	assert.Equal(t, ReasonRateLimit, res.Reason, "429 sans `recipient` doit rester ReasonRateLimit (back-compat vague 6)")
	assert.Equal(t, 429, res.HTTPStatus)
	assert.Empty(t, res.Recipient, "Recipient doit etre vide pour rate_limit global")
	assert.Equal(t, 0, res.RetryAfterSeconds, "RetryAfterSeconds doit etre 0 pour rate_limit global")
}

// stringReader convertit une string en io.Reader (helper test).
func stringReader(s string) io.Reader {
	return &readerOnce{s: s}
}

type readerOnce struct {
	s    string
	done bool
}

func (r *readerOnce) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	n := copy(p, r.s)
	r.done = true
	if n < len(r.s) {
		return n, fmt.Errorf("short read in test helper")
	}
	return n, io.EOF
}
