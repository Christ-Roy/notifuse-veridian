package http

// Tests du handler POST/GET /api/veridian/admin/gc-orphan-workspace-dbs.
// Couvre le garde-fou staging-only (503), le passage des params, et la
// propagation de la réponse / des erreurs. Le DROP réel est testé côté service
// (veridian_orphan_db_gc_test.go) ; ici on isole le handler avec un stub runner.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type stubOrphanGCRunner struct {
	gotInput service.VeridianOrphanDBGCInput
	resp     *service.VeridianOrphanDBGCResponse
	err      error
}

func (s *stubOrphanGCRunner) VeridianGCOrphanWorkspaceDBs(_ context.Context, input service.VeridianOrphanDBGCInput) (*service.VeridianOrphanDBGCResponse, error) {
	s.gotInput = input
	return s.resp, s.err
}

func newOrphanGCHandler() *VeridianHandler {
	return &VeridianHandler{logger: logger.NewLogger()}
}

func postGC(t *testing.T, h *VeridianHandler, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/gc-orphan-workspace-dbs", reader)
	rec := httptest.NewRecorder()
	h.handleGCOrphanWorkspaceDBs(rec, req)
	return rec
}

func TestHandleGC_503WhenNoDeps(t *testing.T) {
	h := newOrphanGCHandler()
	rec := postGC(t, h, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleGC_503WhenNotStaging(t *testing.T) {
	h := newOrphanGCHandler()
	h.SetOrphanDBGC(&stubOrphanGCRunner{}, "production")
	rec := postGC(t, h, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "must be 503 outside staging")
}

func TestHandleGC_503WhenStagingButNilRunner(t *testing.T) {
	h := newOrphanGCHandler()
	h.SetOrphanDBGC(nil, "staging")
	rec := postGC(t, h, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHandleGC_SuccessReturnsResponse(t *testing.T) {
	runner := &stubOrphanGCRunner{
		resp: &service.VeridianOrphanDBGCResponse{
			TotalOrphans: 5,
			Droppable:    3,
			Dropped:      []string{"notifuse_ws_tst1", "notifuse_ws_tst2", "notifuse_ws_tst3"},
			Errors:       map[string]string{},
		},
	}
	h := newOrphanGCHandler()
	h.SetOrphanDBGC(runner, "staging")

	rec := postGC(t, h, map[string]interface{}{"dry_run": false, "max_drops": 10})
	require.Equal(t, http.StatusOK, rec.Code)

	var out service.VeridianOrphanDBGCResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 5, out.TotalOrphans)
	assert.Len(t, out.Dropped, 3)
	assert.Equal(t, 10, runner.gotInput.MaxDrops, "input params must reach the runner")
}

func TestHandleGC_DryRunPassedThrough(t *testing.T) {
	runner := &stubOrphanGCRunner{resp: &service.VeridianOrphanDBGCResponse{DryRun: true}}
	h := newOrphanGCHandler()
	h.SetOrphanDBGC(runner, "staging")

	rec := postGC(t, h, map[string]interface{}{"dry_run": true})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, runner.gotInput.DryRun)
}

func TestHandleGC_EmptyBodyUsesDefaults(t *testing.T) {
	runner := &stubOrphanGCRunner{resp: &service.VeridianOrphanDBGCResponse{}}
	h := newOrphanGCHandler()
	h.SetOrphanDBGC(runner, "staging")

	rec := postGC(t, h, nil)
	assert.Equal(t, http.StatusOK, rec.Code, "empty body must be valid (defaults)")
}

func TestHandleGC_RunnerErrorReturns500(t *testing.T) {
	runner := &stubOrphanGCRunner{err: errors.New("pq: terminating connection due to administrator command")}
	h := newOrphanGCHandler()
	h.SetOrphanDBGC(runner, "staging")

	rec := postGC(t, h, nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Le 500 ne doit pas leak l'erreur Postgres brute au client (message générique).
	assert.NotContains(t, rec.Body.String(), "pq:", "le 500 ne doit pas leak l'erreur Postgres brute")
	assert.Contains(t, rec.Body.String(), "failed to gc orphan workspace dbs")
}
