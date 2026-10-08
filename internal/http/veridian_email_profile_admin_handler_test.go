package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type adminServiceStub struct {
	usageReq domain.VeridianSetUsageRequest
	pauseReq domain.VeridianPauseRequest
	err      error
}

func (s *adminServiceStub) SetUsage(_ context.Context, r domain.VeridianSetUsageRequest) (*domain.VeridianSetUsageResult, error) {
	s.usageReq = r
	if s.err != nil {
		return nil, s.err
	}
	return &domain.VeridianSetUsageResult{IntegrationID: r.IntegrationID, Usage: r.Usage, Rotation: []string{"a"}}, nil
}
func (s *adminServiceStub) Pause(_ context.Context, r domain.VeridianPauseRequest) (*domain.VeridianPauseResult, error) {
	s.pauseReq = r
	if s.err != nil {
		return nil, s.err
	}
	return &domain.VeridianPauseResult{IntegrationID: r.IntegrationID, Paused: true}, nil
}
func (s *adminServiceStub) Resume(_ context.Context, r domain.VeridianPauseRequest) (*domain.VeridianPauseResult, error) {
	s.pauseReq = r
	if s.err != nil {
		return nil, s.err
	}
	return &domain.VeridianPauseResult{IntegrationID: r.IntegrationID, Paused: false}, nil
}

func newAdminHandlerForTest(t *testing.T, svc domain.VeridianEmailProfileAdminService) *VeridianEmailProfileAdminHandler {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianEmailProfileAdminHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
}

func TestVeridianEmailProfileAdminHandler(t *testing.T) {
	t.Run("setUsage", func(t *testing.T) {
		svc := &adminServiceStub{}
		h := newAdminHandlerForTest(t, svc)
		rec := httptest.NewRecorder()
		h.handleSetUsage(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{"workspace_id":"ws1","integration_id":"p1","usage":"transactional"}`)))
		require.Equal(t, nethttp.StatusOK, rec.Code)
		assert.Equal(t, "transactional", svc.usageReq.Usage)
		var out domain.VeridianSetUsageResult
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, "p1", out.IntegrationID)
	})
	t.Run("pause et resume", func(t *testing.T) {
		svc := &adminServiceStub{}
		h := newAdminHandlerForTest(t, svc)
		rec := httptest.NewRecorder()
		h.handlePause(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{"workspace_id":"ws1","integration_id":"p1"}`)))
		require.Equal(t, nethttp.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"paused":true`)
		rec = httptest.NewRecorder()
		h.handleResume(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{"workspace_id":"ws1","integration_id":"p1"}`)))
		require.Equal(t, nethttp.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"paused":false`)
	})
	t.Run("refus de regle = 400 lisible", func(t *testing.T) {
		h := newAdminHandlerForTest(t, &adminServiceStub{err: domain.NewValidationError("a transactional profile cannot be paused")})
		rec := httptest.NewRecorder()
		h.handlePause(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{"workspace_id":"ws1","integration_id":"p1"}`)))
		assert.Equal(t, nethttp.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "cannot be paused")
	})
	t.Run("droit insuffisant = 403, erreur serveur = 500, JSON invalide = 400", func(t *testing.T) {
		h := newAdminHandlerForTest(t, &adminServiceStub{err: domain.NewPermissionError(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite, "no")})
		rec := httptest.NewRecorder()
		h.handleSetUsage(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{"workspace_id":"ws1","integration_id":"p1","usage":"commercial"}`)))
		assert.Equal(t, nethttp.StatusForbidden, rec.Code)
		h = newAdminHandlerForTest(t, &adminServiceStub{err: errors.New("db down")})
		rec = httptest.NewRecorder()
		h.handleSetUsage(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{"workspace_id":"ws1","integration_id":"p1","usage":"commercial"}`)))
		assert.Equal(t, nethttp.StatusInternalServerError, rec.Code)
		rec = httptest.NewRecorder()
		h.handleSetUsage(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(`{`)))
		assert.Equal(t, nethttp.StatusBadRequest, rec.Code)
	})
	t.Run("routes enregistrees en POST seulement", func(t *testing.T) {
		mux := nethttp.NewServeMux()
		newAdminHandlerForTest(t, &adminServiceStub{}).RegisterRoutes(mux)
		for _, p := range []string{"setUsage", "pause", "resume"} {
			_, pattern := mux.Handler(httptest.NewRequest(nethttp.MethodPost, "/api/veridian/emailProfiles."+p, nil))
			assert.Equal(t, "POST /api/veridian/emailProfiles."+p, pattern)
			_, pattern = mux.Handler(httptest.NewRequest(nethttp.MethodGet, "/api/veridian/emailProfiles."+p, nil))
			assert.NotEqual(t, "POST /api/veridian/emailProfiles."+p, pattern)
		}
	})
}
