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

type emailProfileCreateServiceStub struct {
	got    domain.VeridianCreateEmailProfileRequest
	result *domain.VeridianCreateEmailProfileResult
	err    error
}

func (s *emailProfileCreateServiceStub) CreateEmailProfile(_ context.Context, req domain.VeridianCreateEmailProfileRequest) (*domain.VeridianCreateEmailProfileResult, error) {
	s.got = req
	return s.result, s.err
}

func newCreateHandlerForTest(t *testing.T, svc domain.VeridianEmailProfileCreateService) *VeridianEmailProfileCreateHandler {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	return NewVeridianEmailProfileCreateHandler(svc, func() ([]byte, error) { return []byte("secret"), nil }, log)
}

func TestVeridianEmailProfileCreateHandler(t *testing.T) {
	body := `{"workspace_id":"ws1","type":"gmail_app_password","sender_email":"a@gmail.com","app_password":"abcdefghijklmnop"}`

	t.Run("cree et ne renvoie que des identifiants", func(t *testing.T) {
		svc := &emailProfileCreateServiceStub{result: &domain.VeridianCreateEmailProfileResult{IntegrationID: "p1", IMAPIntegrationID: "i1"}}
		h := newCreateHandlerForTest(t, svc)
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/api/veridian/emailProfiles.create", strings.NewReader(body)))
		require.Equal(t, nethttp.StatusCreated, rec.Code)
		assert.Equal(t, "ws1", svc.got.WorkspaceID)
		var out domain.VeridianCreateEmailProfileResult
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, "p1", out.IntegrationID)
		assert.NotContains(t, rec.Body.String(), "abcdefgh")
	})

	t.Run("JSON invalide", func(t *testing.T) {
		h := newCreateHandlerForTest(t, &emailProfileCreateServiceStub{})
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader("{")))
		assert.Equal(t, nethttp.StatusBadRequest, rec.Code)
	})

	t.Run("erreur de validation en 400 avec son message", func(t *testing.T) {
		h := newCreateHandlerForTest(t, &emailProfileCreateServiceStub{err: domain.NewValidationError("Gmail app password must contain exactly 16 characters")})
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(body)))
		assert.Equal(t, nethttp.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "16 characters")
	})

	t.Run("non proprietaire en 403", func(t *testing.T) {
		h := newCreateHandlerForTest(t, &emailProfileCreateServiceStub{err: &domain.ErrUnauthorized{Message: "user is not an owner of the workspace"}})
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(body)))
		assert.Equal(t, nethttp.StatusForbidden, rec.Code)
	})

	t.Run("erreur interne en 500 sans fuite", func(t *testing.T) {
		h := newCreateHandlerForTest(t, &emailProfileCreateServiceStub{err: errors.New("db secret=abcdefghijklmnop")})
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(body)))
		assert.Equal(t, nethttp.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "abcdefgh")
	})
}
