package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

type fakeQueueEntriesService struct {
	listCalls, cancelCalls int
	gotCancel              domain.VeridianQueueCancelRequest
	page                   *domain.VeridianQueueEntriesPage
	result                 *domain.VeridianQueueCancelResult
	err                    error
}

func (f *fakeQueueEntriesService) ListEntries(_ context.Context, _ domain.VeridianQueueEntriesFilter) (*domain.VeridianQueueEntriesPage, error) {
	f.listCalls++
	if f.page == nil {
		f.page = &domain.VeridianQueueEntriesPage{}
	}
	return f.page, f.err
}

func (f *fakeQueueEntriesService) CancelEntries(_ context.Context, req domain.VeridianQueueCancelRequest) (*domain.VeridianQueueCancelResult, error) {
	f.cancelCalls++
	f.gotCancel = req
	if f.result == nil {
		f.result = &domain.VeridianQueueCancelResult{}
	}
	return f.result, f.err
}

func newQueueEntriesHandler(t *testing.T, svc *fakeQueueEntriesService) *VeridianQueueEntriesHandler {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianQueueEntriesHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
}

func TestVeridianQueueEntriesHandler_ListRejectsMissingAutomation(t *testing.T) {
	svc := &fakeQueueEntriesService{}
	h := newQueueEntriesHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleList(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/queue.entries?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, 0, svc.listCalls)
}

func TestVeridianQueueEntriesHandler_ListReturnsPage(t *testing.T) {
	svc := &fakeQueueEntriesService{page: &domain.VeridianQueueEntriesPage{
		Entries: []*domain.VeridianQueueEntry{{ID: "q1", Status: "pending", ContactEmail: "x@y.fr", Reason: "not_examined"}},
	}}
	h := newQueueEntriesHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleList(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/queue.entries?workspace_id=ws1&automation_id=a1&class=google&status=pending", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Entries []map[string]interface{} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Entries, 1)
	assert.Equal(t, "q1", body.Entries[0]["id"])
}

func TestVeridianQueueEntriesHandler_CancelRequiresReason(t *testing.T) {
	svc := &fakeQueueEntriesService{}
	h := newQueueEntriesHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleCancel(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.cancel",
		strings.NewReader(`{"workspace_id":"ws1","automation_id":"a1"}`)))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "pas de motif, pas d'annulation")
	assert.Equal(t, 0, svc.cancelCalls)
}

func TestVeridianQueueEntriesHandler_CancelPassesRequestAndReturnsResult(t *testing.T) {
	svc := &fakeQueueEntriesService{result: &domain.VeridianQueueCancelResult{Matched: 3, Cancelled: 2}}
	h := newQueueEntriesHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleCancel(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.cancel",
		strings.NewReader(`{"workspace_id":"ws1","entry_ids":["e1","e2"],"reason":"retrait client","dry_run":false}`)))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, svc.cancelCalls)
	assert.Equal(t, []string{"e1", "e2"}, svc.gotCancel.EntryIDs)
	assert.Equal(t, "retrait client", svc.gotCancel.Reason)
	var out struct {
		Matched   int `json:"matched"`
		Cancelled int `json:"cancelled"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 3, out.Matched)
	assert.Equal(t, 2, out.Cancelled)
}

func TestVeridianQueueEntriesHandler_CancelPermissionErrorIs403(t *testing.T) {
	svc := &fakeQueueEntriesService{err: domain.NewPermissionError(domain.PermissionResourceAutomations, domain.PermissionTypeWrite, "refus")}
	h := newQueueEntriesHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleCancel(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/queue.cancel",
		strings.NewReader(`{"workspace_id":"ws1","automation_id":"a1","reason":"x"}`)))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
