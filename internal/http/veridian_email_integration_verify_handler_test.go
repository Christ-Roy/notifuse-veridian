package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type stubEmailVerifier struct {
	calls []string
	err   error
}

func (s *stubEmailVerifier) VeridianAdminVerifyEmailIntegration(_ context.Context, ws, integ, to string) error {
	s.calls = append(s.calls, ws+"|"+integ+"|"+to)
	return s.err
}

func postVerify(h *VeridianHandler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handleEmailIntegrationVerify(rec, httptest.NewRequest(http.MethodPost, "/api/veridian/admin/email-integration.verify", strings.NewReader(body)))
	return rec
}

func TestVeridianEmailIntegrationVerify_Handler(t *testing.T) {
	body := `{"workspace_id":"ws","integration_id":"i1","to":"x@y.co"}`

	t.Run("sans verificateur 503", func(t *testing.T) {
		h := &VeridianHandler{logger: logger.NewLogger()}
		assert.Equal(t, http.StatusServiceUnavailable, postVerify(h, body).Code)
	})
	t.Run("ok", func(t *testing.T) {
		v := &stubEmailVerifier{}
		h := &VeridianHandler{logger: logger.NewLogger(), emailVerifier: v}
		rec := postVerify(h, body)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"verified":true`)
		assert.Equal(t, []string{"ws|i1|x@y.co"}, v.calls)
	})
	t.Run("transport en echec : non verifie, pas de fuite", func(t *testing.T) {
		v := &stubEmailVerifier{err: errors.New("smtp auth failed password=hunter2")}
		h := &VeridianHandler{logger: logger.NewLogger(), emailVerifier: v}
		rec := postVerify(h, body)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
		assert.NotContains(t, rec.Body.String(), "hunter2")
		assert.NotContains(t, rec.Body.String(), `"verified":true`)
	})
	t.Run("validation 400", func(t *testing.T) {
		v := &stubEmailVerifier{err: domain.ValidationError{Message: "bad"}}
		h := &VeridianHandler{logger: logger.NewLogger(), emailVerifier: v}
		assert.Equal(t, http.StatusBadRequest, postVerify(h, body).Code)
		assert.Equal(t, http.StatusBadRequest, postVerify(h, "{").Code)
	})
}

// HMAC absent ou JWT owner (Bearer) : refuse AVANT le handler.
func TestVeridianEmailIntegrationVerify_RequiresHMAC(t *testing.T) {
	v := &stubEmailVerifier{}
	h := &VeridianHandler{logger: logger.NewLogger(), emailVerifier: v}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, "test-secret")
	for _, auth := range []string{"", "Bearer eyJhbGciOiJIUzI1NiJ9.e30.x"} {
		req := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/email-integration.verify",
			strings.NewReader(`{"workspace_id":"ws","integration_id":"i1","to":"x@y.co"}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, rec.Code, "auth=%q body=%s", auth, rec.Body.String())
	}
	assert.Empty(t, v.calls, "le verificateur n'est jamais atteint sans HMAC")
}
