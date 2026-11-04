package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type verifyServiceStub struct {
	got [3]string
	err error
}

func (s *verifyServiceStub) VerifyEmailProfile(_ context.Context, ws, id, to string) error {
	s.got = [3]string{ws, id, to}
	return s.err
}

func TestVeridianEmailProfileVerifyHandler_Statuses(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()

	call := func(err error, body string) (*httptest.ResponseRecorder, *verifyServiceStub) {
		stub := &verifyServiceStub{err: err}
		h := NewVeridianEmailProfileVerifyHandler(stub, func() ([]byte, error) { return []byte("secret"), nil }, log)
		rec := httptest.NewRecorder()
		h.handle(rec, httptest.NewRequest(nethttp.MethodPost, "/x", strings.NewReader(body)))
		return rec, stub
	}
	const ok = `{"workspace_id":"ws","integration_id":"p1","to":"me@client.fr"}`

	rec, stub := call(nil, ok)
	assert.Equal(t, nethttp.StatusOK, rec.Code)
	assert.Equal(t, [3]string{"ws", "p1", "me@client.fr"}, stub.got)
	assert.Contains(t, rec.Body.String(), `"verified":true`)

	rec, _ = call(&domain.ErrUnauthorized{Message: "not a member"}, ok)
	assert.Equal(t, nethttp.StatusForbidden, rec.Code, "cle d'un autre workspace ou lecture seule")
	rec, _ = call(&domain.ErrAuthenticationFailed{Message: "revoked"}, ok)
	assert.Equal(t, nethttp.StatusUnauthorized, rec.Code)
	rec, _ = call(domain.NewValidationError("bad"), ok)
	assert.Equal(t, nethttp.StatusBadRequest, rec.Code)
	rec, _ = call(service.ErrVeridianVerifyRateLimited, ok)
	assert.Equal(t, nethttp.StatusTooManyRequests, rec.Code)
	rec, _ = call(nil, `{not json`)
	assert.Equal(t, nethttp.StatusBadRequest, rec.Code)

	rec, _ = call(errors.New("535 auth failed for secret-user"), ok)
	assert.Equal(t, nethttp.StatusBadGateway, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret-user", "l'erreur SMTP brute ne sort jamais")
}
