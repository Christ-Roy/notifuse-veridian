package http

// === Veridian patch ===
// Tests unitaires VeridianMagicHandler — endpoint
// POST /api/workspaces.generateMagicLink (auth API key tenant Notifuse).
//
// Le handler attend dans le ctx :
//   - domain.UserTypeKey (string) : "api_key" obligatoire
//   - domain.UserIDKey (string)   : id du user api_key
// Il appelle workspaceRepo.GetUserWorkspaces(apiUserID), exige 1 workspace,
// puis service.GenerateMagicLink(workspaceID, email).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMagicHandler(svc domain.VeridianService, repo domain.WorkspaceRepository) *VeridianMagicHandler {
	return &VeridianMagicHandler{
		service:       svc,
		workspaceRepo: repo,
		getJWTSecret:  func() ([]byte, error) { return []byte("test-jwt-secret"), nil },
		logger:        logger.NewLogger(),
	}
}

// reqWithAuthCtx construit une req POST /api/workspaces.generateMagicLink
// avec un ctx pre-popule (userType=api_key, userID=apiUserID).
func reqWithAuthCtx(t *testing.T, body, userType, userID string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces.generateMagicLink", bytes.NewReader([]byte(body)))
	ctx := context.WithValue(r.Context(), domain.UserTypeKey, userType)
	ctx = context.WithValue(ctx, domain.UserIDKey, userID)
	return r.WithContext(ctx)
}

func TestVeridianMagicLink_OK(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)

	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{
		{UserID: "api-user-1", WorkspaceID: "ws-1"},
	}, nil)
	svc.EXPECT().GenerateMagicLink(gomock.Any(), "ws-1", "u@x.test").Return(&domain.MagicLinkResponse{
		MagicLink:    "https://example.com/console/signin?code=123&email=u%40x.test",
		AutoLoginURL: "https://example.com/veridian/auto-login?token=abc",
		ExpiresAt:    time.Now().Add(time.Hour),
	}, nil)

	h := newMagicHandler(svc, repo)
	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.MagicLinkResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Contains(t, resp.MagicLink, "code=123")
	assert.Contains(t, resp.AutoLoginURL, "/veridian/auto-login?token=")
}

func TestVeridianMagicLink_GETNotAllowed_405(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	h := newMagicHandler(svc, repo)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces.generateMagicLink", nil)
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestVeridianMagicLink_UserType_NotAPIKey_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	h := newMagicHandler(svc, repo)

	// userType=user → forbidden, ce endpoint exige api_key
	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeUser), "user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "API key")
}

func TestVeridianMagicLink_MissingUserID_401(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	h := newMagicHandler(svc, repo)

	// userType=api_key mais userID vide
	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeAPIKey), "")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "missing user id")
}

func TestVeridianMagicLink_WorkspaceLookupFails_500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return(nil, errors.New("db down"))
	h := newMagicHandler(svc, repo)

	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "failed to resolve workspace")
}

func TestVeridianMagicLink_NoWorkspace_403(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{}, nil)
	h := newMagicHandler(svc, repo)

	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "not attached to any workspace")
}

func TestVeridianMagicLink_MultipleWorkspaces_409(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{
		{UserID: "api-user-1", WorkspaceID: "ws-1"},
		{UserID: "api-user-1", WorkspaceID: "ws-2"},
	}, nil)
	h := newMagicHandler(svc, repo)

	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ambiguous")
}

func TestVeridianMagicLink_InvalidJSON_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{
		{UserID: "api-user-1", WorkspaceID: "ws-1"},
	}, nil)
	h := newMagicHandler(svc, repo)

	req := reqWithAuthCtx(t, `not json`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON body")
}

func TestVeridianMagicLink_MissingUserEmail_400(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{
		{UserID: "api-user-1", WorkspaceID: "ws-1"},
	}, nil)
	h := newMagicHandler(svc, repo)

	req := reqWithAuthCtx(t, `{}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "user_email is required")
}

func TestVeridianMagicLink_ServiceErrUserNotFound_404(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{
		{UserID: "api-user-1", WorkspaceID: "ws-1"},
	}, nil)
	svc.EXPECT().GenerateMagicLink(gomock.Any(), "ws-1", "ghost@x.test").Return(nil, &domain.ErrUserNotFound{Message: "ghost@x.test"})

	h := newMagicHandler(svc, repo)
	req := reqWithAuthCtx(t, `{"user_email":"ghost@x.test"}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "user not found")
}

func TestVeridianMagicLink_ServiceGenericError_500(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	repo.EXPECT().GetUserWorkspaces(gomock.Any(), "api-user-1").Return([]*domain.UserWorkspace{
		{UserID: "api-user-1", WorkspaceID: "ws-1"},
	}, nil)
	svc.EXPECT().GenerateMagicLink(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("internal upstream"))

	h := newMagicHandler(svc, repo)
	req := reqWithAuthCtx(t, `{"user_email":"u@x.test"}`, string(domain.UserTypeAPIKey), "api-user-1")
	rec := httptest.NewRecorder()
	h.handleGenerateMagicLink(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianMagicLink_RegisterRoutes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	svc := mocks.NewMockVeridianService(ctrl)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	h := NewVeridianMagicHandler(svc, repo, func() ([]byte, error) { return []byte("test"), nil }, logger.NewLogger())

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces.generateMagicLink", nil)
	_, pattern := mux.Handler(req)
	assert.NotEmpty(t, pattern, "POST /api/workspaces.generateMagicLink should be mounted")
}
