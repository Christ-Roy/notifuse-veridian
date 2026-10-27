package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

type fakeEnrollmentsService struct {
	calls int
	got   domain.VeridianAutomationEnrollmentFilter
	page  *domain.VeridianEnrollmentPage
	err   error
}

func (f *fakeEnrollmentsService) ListEnrollments(_ context.Context, fl domain.VeridianAutomationEnrollmentFilter) (*domain.VeridianEnrollmentPage, error) {
	f.calls++
	f.got = fl
	return f.page, f.err
}

func newEnrollmentsHandler(t *testing.T, svc *fakeEnrollmentsService) *VeridianAutomationEnrollmentsListHandler {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianAutomationEnrollmentsListHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
}

func TestVeridianAutomationEnrollmentsHandler_RejectsMissingAutomation(t *testing.T) {
	svc := &fakeEnrollmentsService{}
	h := newEnrollmentsHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleList(rec, httptest.NewRequest(http.MethodGet, "/api/automations.enrollments?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, 0, svc.calls, "pas d'appel au service sans automation_id")
}

func TestVeridianAutomationEnrollmentsHandler_RejectsBadLimitAndStatus(t *testing.T) {
	svc := &fakeEnrollmentsService{}
	h := newEnrollmentsHandler(t, svc)
	for _, path := range []string{
		"/api/automations.enrollments?workspace_id=ws1&automation_id=a1&limit=abc",
		"/api/automations.enrollments?workspace_id=ws1&automation_id=a1&limit=9999",
		"/api/automations.enrollments?workspace_id=ws1&automation_id=a1&status=bogus",
	} {
		rec := httptest.NewRecorder()
		h.handleList(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
	assert.Equal(t, 0, svc.calls)
}

func TestVeridianAutomationEnrollmentsHandler_ReturnsPageAndCursor(t *testing.T) {
	svc := &fakeEnrollmentsService{page: &domain.VeridianEnrollmentPage{
		Enrollments: []*domain.ContactAutomation{{ID: "ca1", AutomationID: "a1", ContactEmail: "x@y.fr", Status: domain.ContactAutomationStatusActive}},
		NextCursor:  "curseur-suivant",
	}}
	h := newEnrollmentsHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleList(rec, httptest.NewRequest(http.MethodGet, "/api/automations.enrollments?workspace_id=ws1&automation_id=a1&status=active&limit=10&cursor=MjAyNi0xMC0yN1QwOTozMDoxNVp8Y2Ew", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 10, svc.got.Limit)
	assert.Equal(t, "active", svc.got.Status)
	var body struct {
		Enrollments []map[string]interface{} `json:"enrollments"`
		NextCursor  string                   `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Enrollments, 1)
	assert.Equal(t, "ca1", body.Enrollments[0]["id"])
	assert.Equal(t, "curseur-suivant", body.NextCursor)
}

func TestVeridianAutomationEnrollmentsHandler_PermissionErrorIs403(t *testing.T) {
	svc := &fakeEnrollmentsService{err: domain.NewPermissionError(domain.PermissionResourceAutomations, domain.PermissionTypeRead, "refus")}
	h := newEnrollmentsHandler(t, svc)
	rec := httptest.NewRecorder()
	h.handleList(rec, httptest.NewRequest(http.MethodGet, "/api/automations.enrollments?workspace_id=ws1&automation_id=a1", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
