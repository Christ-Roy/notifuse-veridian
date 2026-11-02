package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const exportSentinelPassword = "PW-SENTINEL-EXPORT-123"

const exportRoute = "/api/veridian/admin/email-integration.export"

type stubIntegrationExporter struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubIntegrationExporter) VeridianAdminExportIntegrations(_ context.Context, ws, integ string) (*service.VeridianIntegrationExport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &service.VeridianIntegrationExport{
		WorkspaceID: ws,
		Exported: []service.VeridianExportedIntegration{{
			IntegrationID: "smtp1", Name: "SMTP", Type: "smtp", Host: "h", Port: 587,
			Username: "u", Password: exportSentinelPassword,
		}},
		NotExportable: []service.VeridianNotExportable{{IntegrationID: "llm1", Name: "LLM", Type: "llm", Reason: "non exportable"}},
	}, nil
}

func (s *stubIntegrationExporter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// recordingExportLogger capture chaque ligne (message + champs) pour vérifier l'audit.
type recordingExportLogger struct {
	sink   *exportLogSink
	fields map[string]interface{}
}

type exportLogSink struct {
	mu      sync.Mutex
	entries []string
}

func newRecordingExportLogger() *recordingExportLogger {
	return &recordingExportLogger{sink: &exportLogSink{}}
}

func (l *recordingExportLogger) record(msg string) {
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	l.sink.entries = append(l.sink.entries, fmt.Sprintf("%s %v", msg, l.fields))
}

func (l *recordingExportLogger) Debug(msg string) {}
func (l *recordingExportLogger) Info(msg string)  { l.record(msg) }
func (l *recordingExportLogger) Warn(msg string)  { l.record(msg) }
func (l *recordingExportLogger) Error(msg string) { l.record(msg) }
func (l *recordingExportLogger) Fatal(msg string) { l.record(msg) }

func (l *recordingExportLogger) WithField(key string, value interface{}) logger.Logger {
	return l.WithFields(map[string]interface{}{key: value})
}

func (l *recordingExportLogger) WithFields(fields map[string]interface{}) logger.Logger {
	merged := map[string]interface{}{}
	for k, v := range l.fields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	return &recordingExportLogger{sink: l.sink, fields: merged}
}

func (l *recordingExportLogger) dump() string {
	l.sink.mu.Lock()
	defer l.sink.mu.Unlock()
	return strings.Join(l.sink.entries, "\n")
}

func postExport(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, exportRoute, strings.NewReader(body))
	h.ServeHTTP(rec, req)
	return rec
}

func signHub(secret, body string) (string, string) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return ts, hex.EncodeToString(mac.Sum(nil))
}

func TestVeridianIntegrationExport_Handler(t *testing.T) {
	body := `{"workspace_id":"ws-export","integration_id":"smtp1"}`

	t.Run("sans exporteur : 503", func(t *testing.T) {
		h := &VeridianHandler{logger: logger.NewLogger()}
		assert.Equal(t, http.StatusServiceUnavailable, postExport(http.HandlerFunc(h.handleEmailIntegrationExport), body).Code)
	})

	t.Run("ok : SMTP en clair, no-store, pas de cache", func(t *testing.T) {
		stub := &stubIntegrationExporter{}
		h := &VeridianHandler{logger: logger.NewLogger()}
		h.SetIntegrationExporter(stub)
		rec := postExport(http.HandlerFunc(h.handleEmailIntegrationExport), body)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		assert.Equal(t, "no-cache", rec.Header().Get("Pragma"))
		assert.Contains(t, rec.Body.String(), exportSentinelPassword)
		assert.Contains(t, rec.Body.String(), `"not_exportable"`)
		assert.Equal(t, 1, stub.count())
	})

	t.Run("JSON invalide : 400, exporteur non appelé", func(t *testing.T) {
		stub := &stubIntegrationExporter{}
		h := &VeridianHandler{logger: logger.NewLogger()}
		h.SetIntegrationExporter(stub)
		assert.Equal(t, http.StatusBadRequest, postExport(http.HandlerFunc(h.handleEmailIntegrationExport), "{").Code)
		assert.Equal(t, 0, stub.count())
	})

	t.Run("workspace_id manquant : 400", func(t *testing.T) {
		stub := &stubIntegrationExporter{}
		h := &VeridianHandler{logger: logger.NewLogger()}
		h.SetIntegrationExporter(stub)
		assert.Equal(t, http.StatusBadRequest, postExport(http.HandlerFunc(h.handleEmailIntegrationExport), `{"integration_id":"x"}`).Code)
		assert.Equal(t, 0, stub.count())
	})

	t.Run("workspace inconnu : 404 sans secret", func(t *testing.T) {
		stub := &stubIntegrationExporter{err: &domain.ErrWorkspaceNotFound{WorkspaceID: "ws-export"}}
		h := &VeridianHandler{logger: logger.NewLogger()}
		h.SetIntegrationExporter(stub)
		rec := postExport(http.HandlerFunc(h.handleEmailIntegrationExport), body)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "tenant_not_found")
	})

	t.Run("integration inconnue : 404", func(t *testing.T) {
		stub := &stubIntegrationExporter{err: service.ErrVeridianExportIntegrationNotFound}
		h := &VeridianHandler{logger: logger.NewLogger()}
		h.SetIntegrationExporter(stub)
		assert.Equal(t, http.StatusNotFound, postExport(http.HandlerFunc(h.handleEmailIntegrationExport), body).Code)
	})

	t.Run("erreur interne : 500 generique, aucune trace du detail", func(t *testing.T) {
		stub := &stubIntegrationExporter{err: errors.New("decrypt failed password=hunter2")}
		h := &VeridianHandler{logger: logger.NewLogger()}
		h.SetIntegrationExporter(stub)
		rec := postExport(http.HandlerFunc(h.handleEmailIntegrationExport), body)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "hunter2")
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	})
}

// Sans HMAC valide : refus AVANT le handler. Un JWT owner ou une clé API (Bearer) est refusé.
// Une signature valide passe. Si le middleware HMAC est retiré de la route, ce test est rouge.
func TestVeridianIntegrationExport_RequiresHMAC(t *testing.T) {
	const secret = "test-hub-secret"
	body := `{"workspace_id":"ws-export"}`

	stub := &stubIntegrationExporter{}
	h := &VeridianHandler{logger: logger.NewLogger()}
	h.SetIntegrationExporter(stub)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, secret)

	for _, auth := range []string{"", "Bearer eyJhbGciOiJIUzI1NiJ9.e30.x", "Bearer sk_live_cle_client"} {
		req := httptest.NewRequest(http.MethodPost, exportRoute, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code, "auth=%q", auth)
		assert.NotContains(t, rec.Body.String(), exportSentinelPassword)
	}
	assert.Equal(t, 0, stub.count(), "l'exporteur n'est jamais atteint sans HMAC")

	ts, sig := signHub(secret, body)
	req := httptest.NewRequest(http.MethodPost, exportRoute, strings.NewReader(body))
	req.Header.Set("X-Veridian-Timestamp", ts)
	req.Header.Set("X-Veridian-Hub-Signature", sig)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, stub.count())
}

// Rate limit dédié : 20 appels par heure, le 21e est refusé sans atteindre l'exporteur.
func TestVeridianIntegrationExport_RateLimit(t *testing.T) {
	stub := &stubIntegrationExporter{}
	h := &VeridianHandler{logger: logger.NewLogger()}
	h.SetIntegrationExporter(stub)
	fn := http.HandlerFunc(h.handleEmailIntegrationExport)
	body := `{"workspace_id":"ws-export"}`

	for i := 0; i < veridianIntegrationExportMaxPerWindow; i++ {
		require.Equal(t, http.StatusOK, postExport(fn, body).Code, "appel %d", i+1)
	}
	rec := postExport(fn, body)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), "rate_limited")
	assert.Equal(t, veridianIntegrationExportMaxPerWindow, stub.count())
}

func TestVeridianExportRateLimiter_Window(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	l := newVeridianExportRateLimiter(2, time.Hour)
	l.now = func() time.Time { return now }
	assert.True(t, l.allow())
	assert.True(t, l.allow())
	assert.False(t, l.allow(), "fenêtre pleine")
	now = now.Add(time.Hour + time.Second)
	assert.True(t, l.allow(), "fenêtre glissante : les appels anciens sortent")
}

// Audit : une ligne par appel, succès ET échec, et aucun secret dans aucune ligne.
func TestVeridianIntegrationExport_Audit(t *testing.T) {
	rl := newRecordingExportLogger()
	stub := &stubIntegrationExporter{}
	h := &VeridianHandler{logger: rl}
	h.SetIntegrationExporter(stub)
	fn := http.HandlerFunc(h.handleEmailIntegrationExport)

	req := httptest.NewRequest(http.MethodPost, exportRoute, strings.NewReader(`{"workspace_id":"ws-export","integration_id":"smtp1"}`))
	req.RemoteAddr = "10.0.0.7:4242"
	rec := httptest.NewRecorder()
	fn.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodPost, exportRoute, strings.NewReader("{"))
	req.RemoteAddr = "10.0.0.7:4243"
	fn.ServeHTTP(httptest.NewRecorder(), req)

	dump := rl.dump()
	assert.Contains(t, dump, "veridian_integration_export")
	assert.Contains(t, dump, "outcome:ok")
	assert.Contains(t, dump, "outcome:invalid")
	assert.Contains(t, dump, "workspace_id:ws-export")
	assert.Contains(t, dump, "integration_id:smtp1")
	assert.Contains(t, dump, "origin:10.0.0.7")
	assert.NotContains(t, dump, exportSentinelPassword, "le journal ne porte jamais un secret")
	assert.Equal(t, 2, len(rl.sink.entries))
}

func TestVeridianExportOrigin(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, exportRoute, nil)
	r.RemoteAddr = "192.0.2.10:55555"
	assert.Equal(t, "192.0.2.10", veridianExportOrigin(r))
	r.RemoteAddr = "pas-un-hote-port"
	assert.Equal(t, "pas-un-hote-port", veridianExportOrigin(r))
}

// SetIntegrationExporter injecte l'exporteur ET arme le rate limit dédié.
func TestVeridianIntegrationExport_SetIntegrationExporter(t *testing.T) {
	h := &VeridianHandler{logger: logger.NewLogger()}
	assert.Nil(t, h.integrationExportLimiter)
	h.SetIntegrationExporter(&stubIntegrationExporter{})
	require.NotNil(t, h.integrationExporter)
	require.NotNil(t, h.integrationExportLimiter)
	assert.True(t, h.integrationExportLimiter.allow())
}
