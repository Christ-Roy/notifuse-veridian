package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianWebhookEmitter_NoopIfDisabled(t *testing.T) {
	log := logger.NewLogger()

	emitter := NewVeridianWebhookEmitter("", "secret", log)
	require.NotNil(t, emitter)
	_, isNoop := emitter.(*noopWebhookEmitter)
	assert.True(t, isNoop, "expected noop emitter when hubURL is empty")

	emitter2 := NewVeridianWebhookEmitter("https://hub.example", "", log)
	_, isNoop2 := emitter2.(*noopWebhookEmitter)
	assert.True(t, isNoop2, "expected noop emitter when hubSecret is empty")

	// Emit ne doit rien panic
	emitter.Emit(t.Context(), domain.EventTenantSuspended, "ws-1", map[string]interface{}{"k": "v"})
}

func TestVeridianWebhookEmitter_SendsHTTPPost(t *testing.T) {
	const secret = "test-secret"
	type captured struct {
		body []byte
		sig  string
		ts   string
		ct   string
	}
	ch := make(chan captured, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		ch <- captured{
			body: body,
			sig:  r.Header.Get(veridianWebhookSignatureHdr),
			ts:   r.Header.Get(veridianWebhookTimestampHdr),
			ct:   r.Header.Get("Content-Type"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	emitter := NewVeridianWebhookEmitter(server.URL, secret, logger.NewLogger())
	emitter.Emit(t.Context(), domain.EventTenantProvisioned, "ws-42", map[string]interface{}{
		"plan": "pro",
	})

	var got captured
	select {
	case got = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HTTP request")
	}

	assert.Equal(t, "application/json", got.ct)
	assert.NotEmpty(t, got.sig)
	assert.NotEmpty(t, got.ts)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(got.ts))
	mac.Write([]byte("."))
	mac.Write(got.body)
	expected := hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, expected, got.sig, "signature HMAC must match")

	tsMs, err := strconv.ParseInt(got.ts, 10, 64)
	require.NoError(t, err)
	drift := time.Since(time.UnixMilli(tsMs))
	assert.True(t, drift < 5*time.Second && drift > -5*time.Second, "timestamp drift too high: %v", drift)

	var payload domain.VeridianEventPayload
	require.NoError(t, json.Unmarshal(got.body, &payload))
	assert.Equal(t, domain.EventTenantProvisioned, payload.EventType)
	assert.Equal(t, "ws-42", payload.TenantID)
	assert.NotEmpty(t, payload.EventID)
	assert.Equal(t, "pro", payload.Data["plan"])
	assert.False(t, payload.OccurredAt.IsZero())
}

func TestVeridianWebhookEmitter_RetriesOn5xx(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	e := &veridianWebhookEmitter{
		hubURL:    server.URL,
		hubSecret: "secret",
		logger:    logger.NewLogger(),
		client:    &http.Client{Timeout: veridianWebhookTimeout},
	}
	e.Emit(t.Context(), domain.EventEmailBounced, "ws-1", nil)

	require.Eventually(t, func() bool { return calls.Load() >= 2 }, 5*time.Second, 20*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(2), calls.Load(), "should stop after first success")
}

func TestVeridianWebhookEmitter_GivesUpAfter3Retries(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e := &veridianWebhookEmitter{
		hubURL:    server.URL,
		hubSecret: "secret",
		logger:    logger.NewLogger(),
		client:    &http.Client{Timeout: veridianWebhookTimeout},
	}
	e.Emit(t.Context(), domain.EventTenantDeleted, "ws-1", nil)

	require.Eventually(t, func() bool { return calls.Load() >= 3 }, 10*time.Second, 20*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(3), calls.Load(), "should attempt exactly 3 times")
}

func TestVeridianWebhookEmitter_NoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	e := &veridianWebhookEmitter{
		hubURL:    server.URL,
		hubSecret: "secret",
		logger:    logger.NewLogger(),
		client:    &http.Client{Timeout: veridianWebhookTimeout},
	}
	e.Emit(t.Context(), domain.EventEmailComplaint, "ws-1", nil)

	// Attendre largement assez pour que les retries auraient pu partir
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load(), "4xx must not be retried")
}

func TestVeridianWebhookEmitter_DoesNotBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	emitter := NewVeridianWebhookEmitter(server.URL, "secret", logger.NewLogger())

	start := time.Now()
	emitter.Emit(t.Context(), domain.EventEmailSent, "ws-1", map[string]interface{}{"big": "data"})
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 50*time.Millisecond, "Emit must return immediately, took %v", elapsed)
}
