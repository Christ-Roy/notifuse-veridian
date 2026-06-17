package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func newEngagementByClassHandler(ctrl *gomock.Controller) (*VeridianEngagementByClassHandler, *mocks.MockVeridianEngagementByClassService) {
	svc := mocks.NewMockVeridianEngagementByClassService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	h := NewVeridianEngagementByClassHandler(svc, func() ([]byte, error) { return []byte("test-secret"), nil }, log)
	return h, svc
}

func sampleEngagement() *domain.VeridianEngagementByClass {
	return &domain.VeridianEngagementByClass{
		ByClass: map[string]domain.VeridianClassEngagement{
			domain.ProviderClassGoogle: {Sent: 10, Delivered: 9, Bounced: 1, Opened: 4, Clicked: 1},
		},
		Total: domain.VeridianClassEngagement{Sent: 10, Delivered: 9, Bounced: 1, Opened: 4, Clicked: 1},
	}
}

func TestVeridianEngagementByClassHandler_POST(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newEngagementByClassHandler(ctrl)

	wantSince := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC) // end 2026-06-15 + 1j
	svc.EXPECT().GetEngagementByClass(gomock.Any(), &domain.VeridianEngagementByClassRequest{
		WorkspaceID: "ws123",
		Since:       wantSince,
		Until:       wantUntil,
	}).Return(sampleEngagement(), nil)

	body, _ := json.Marshal(map[string]string{"workspace_id": "ws123", "start": "2026-06-01", "end": "2026-06-15"})
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/messages.engagementByClass", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleEngagementByClass(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp domain.VeridianEngagementByClass
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 10, resp.ByClass[domain.ProviderClassGoogle].Sent)
	assert.Equal(t, 10, resp.Total.Sent)
}

func TestVeridianEngagementByClassHandler_GET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newEngagementByClassHandler(ctrl)
	svc.EXPECT().GetEngagementByClass(gomock.Any(), &domain.VeridianEngagementByClassRequest{
		WorkspaceID: "ws123",
	}).Return(sampleEngagement(), nil)

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.engagementByClass?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianEngagementByClassHandler_MissingWorkspaceID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newEngagementByClassHandler(ctrl)
	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.engagementByClass", nil)
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianEngagementByClassHandler_InvalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newEngagementByClassHandler(ctrl)
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/messages.engagementByClass", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianEngagementByClassHandler_InvalidStartDate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newEngagementByClassHandler(ctrl)
	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.engagementByClass?workspace_id=ws123&start=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianEngagementByClassHandler_PermissionError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newEngagementByClassHandler(ctrl)
	svc.EXPECT().GetEngagementByClass(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewPermissionError(domain.PermissionResourceContacts, domain.PermissionTypeRead, "no read"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.engagementByClass?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestVeridianEngagementByClassHandler_AuthFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newEngagementByClassHandler(ctrl)
	svc.EXPECT().GetEngagementByClass(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("failed to authenticate user: not a member"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.engagementByClass?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianEngagementByClassHandler_InternalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, svc := newEngagementByClassHandler(ctrl)
	svc.EXPECT().GetEngagementByClass(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("db down"))

	r := httptest.NewRequest(http.MethodGet, "/api/veridian/messages.engagementByClass?workspace_id=ws123", nil)
	rec := httptest.NewRecorder()
	h.handleEngagementByClass(rec, r)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianEngagementByClassHandler_RegisterRoutes(t *testing.T) {
	// Vérifie que POST ET GET sont routés explicitement (piège catchall
	// root_handler.go). Sans Authorization header, RequireAuth renvoie 401 →
	// 401 (et NON 404/405) prouve que les deux méthodes atteignent le middleware.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	h, _ := newEngagementByClassHandler(ctrl)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/veridian/messages.engagementByClass?workspace_id=ws123", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "method %s should reach RequireAuth (401), not the catchall", method)
		assert.NotEqual(t, http.StatusNotFound, rec.Code, "method %s not routed (catchall trap)", method)
		assert.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, "method %s rejected by mux", method)
	}
}
