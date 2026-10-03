package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emailProfileUsageServiceStub struct {
	workspaceID string
	result      *domain.VeridianEmailProfilesUsage
}

func (s *emailProfileUsageServiceStub) GetEmailProfilesUsage(_ context.Context, workspaceID string) (*domain.VeridianEmailProfilesUsage, error) {
	s.workspaceID = workspaceID
	return s.result, nil
}

func TestVeridianEmailProfileUsageHandlerGET(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := pkgmocks.NewMockLogger(ctrl)
	svc := &emailProfileUsageServiceStub{result: &domain.VeridianEmailProfilesUsage{Date: "2026-08-05", TotalUsed: 4}}
	h := NewVeridianEmailProfileUsageHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
	req := httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.usage?workspace_id=ws1", nil)
	rec := httptest.NewRecorder()
	h.handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ws1", svc.workspaceID)
	var body domain.VeridianEmailProfilesUsage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 4, body.TotalUsed)
}

func TestVeridianEmailProfileUsageHandlerRequiresWorkspace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := pkgmocks.NewMockLogger(ctrl)
	h := NewVeridianEmailProfileUsageHandler(&emailProfileUsageServiceStub{}, func() ([]byte, error) { return []byte("secret"), nil }, log)
	rec := httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.usage", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// emailProfileUsageServiceErrStub simule un service dont GetEmailProfilesUsage
// echoue -- utilise pour prouver que l'erreur typee est classee en 401/403 et
// non ecrasee en 500 (mission 2026-10-03 "401/403 partout").
type emailProfileUsageServiceErrStub struct {
	err error
}

func (s *emailProfileUsageServiceErrStub) GetEmailProfilesUsage(_ context.Context, _ string) (*domain.VeridianEmailProfilesUsage, error) {
	return nil, s.err
}

func TestVeridianEmailProfileUsageHandler_AuthFailure_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := &emailProfileUsageServiceErrStub{err: &domain.ErrAuthenticationFailed{Message: "api key revoked"}}
	h := NewVeridianEmailProfileUsageHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
	rec := httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.usage?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVeridianEmailProfileUsageHandler_PermissionDenied_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := &emailProfileUsageServiceErrStub{err: domain.NewPermissionError(domain.PermissionResourceWorkspace, domain.PermissionTypeRead, "read access required")}
	h := NewVeridianEmailProfileUsageHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
	rec := httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.usage?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
