package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emailProfileOverviewServiceStub struct {
	workspaceID string
	result      *domain.VeridianEmailProfilesOverview
	err         error
}

func (s *emailProfileOverviewServiceStub) GetEmailProfilesOverview(_ context.Context, workspaceID string) (*domain.VeridianEmailProfilesOverview, error) {
	s.workspaceID = workspaceID
	return s.result, s.err
}

func newOverviewHandlerForTest(t *testing.T, svc domain.VeridianEmailProfileOverviewService) *VeridianEmailProfileOverviewHandler {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianEmailProfileOverviewHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
}

func TestVeridianEmailProfileOverviewHandlerGETAndPOST(t *testing.T) {
	svc := &emailProfileOverviewServiceStub{result: &domain.VeridianEmailProfilesOverview{Date: "2026-10-08", Profiles: []domain.VeridianEmailProfileOverview{{IntegrationID: "p1"}}}}
	h := newOverviewHandlerForTest(t, svc)

	rec := httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.overview?workspace_id=ws1", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ws1", svc.workspaceID)
	var body domain.VeridianEmailProfilesOverview
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "p1", body.Profiles[0].IntegrationID)

	svc.workspaceID = ""
	rec = httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/emailProfiles.overview", strings.NewReader(`{"workspace_id":"ws2"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ws2", svc.workspaceID)
}

func TestVeridianEmailProfileOverviewHandlerValidation(t *testing.T) {
	h := newOverviewHandlerForTest(t, &emailProfileOverviewServiceStub{})

	rec := httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.overview", nil))
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	h.handle(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/emailProfiles.overview", strings.NewReader(`{not json`)))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVeridianEmailProfileOverviewHandlerClassifiesErrors(t *testing.T) {
	forbidden := newOverviewHandlerForTest(t, &emailProfileOverviewServiceStub{err: domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "no read")})
	rec := httptest.NewRecorder()
	forbidden.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.overview?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code, "permission manquante = 403, pas 500")

	broken := newOverviewHandlerForTest(t, &emailProfileOverviewServiceStub{err: errors.New("db down")})
	rec = httptest.NewRecorder()
	broken.handle(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.overview?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianEmailProfileOverviewHandlerRegistersGetAndPostRoutes(t *testing.T) {
	h := newOverviewHandlerForTest(t, &emailProfileOverviewServiceStub{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/api/veridian/emailProfiles.overview?workspace_id=ws1", nil)
		_, pattern := mux.Handler(req)
		assert.Equal(t, method+" /api/veridian/emailProfiles.overview", pattern)
	}
	// Sans jeton, l'accès est refusé : la route n'est pas publique.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/veridian/emailProfiles.overview?workspace_id=ws1", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
