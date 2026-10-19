package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
)

type fakeAuthz struct {
	user *domain.User
	uw   *domain.UserWorkspace
	err  error
	seen []string
}

func (f *fakeAuthz) AuthenticateUserForWorkspace(ctx context.Context, ws string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	f.seen = append(f.seen, ws)
	return ctx, f.user, f.uw, f.err
}

func authzToken(t *testing.T, typ string) string {
	claims := &service.UserClaims{UserID: "u1", Type: typ, SessionID: "s1"}
	claims.RegisteredClaims = jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("sec"))
	require.NoError(t, err)
	return tok
}

func runAuthz(t *testing.T, f *fakeAuthz, method, path, body string) (*httptest.ResponseRecorder, *bool) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(200) })
	h := VeridianWorkspaceAuthzMiddleware(f, func() ([]byte, error) { return []byte("sec"), nil })(next)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+authzToken(t, "user"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, &reached
}

func TestVeridianWorkspaceAuthz(t *testing.T) {
	member := &fakeAuthz{user: &domain.User{ID: "k"}, uw: &domain.UserWorkspace{Role: "member", Permissions: domain.FullPermissions}}
	owner := &fakeAuthz{user: &domain.User{ID: "o", Type: domain.UserTypeUser}, uw: &domain.UserWorkspace{Role: "owner"}}
	foreign := &fakeAuthz{err: &domain.ErrUnauthorized{Message: "not authorized for this workspace"}}

	t.Run("autre workspace : 403, handler jamais atteint", func(t *testing.T) {
		for _, c := range [][3]string{
			{"GET", "/api/webhookSubscriptions.list?workspace_id=victim", ""},
			{"POST", "/api/segments.rebuild", `{"workspace_id":"victim","segment_id":"s"}`},
			{"GET", "/api/timeline.list?workspace_id=victim&email=a@b.co", ""},
			{"POST", "/api/webhookSubscriptions.delete", `{"workspace_id":"victim","id":"x"}`},
		} {
			f := &fakeAuthz{err: foreign.err}
			rec, reached := runAuthz(t, f, c[0], c[1], c[2])
			assert.Equal(t, http.StatusForbidden, rec.Code, c[1])
			assert.False(t, *reached, c[1])
			assert.Equal(t, []string{"victim"}, f.seen)
		}
	})
	t.Run("gestion webhooks : cle member 403, owner OK", func(t *testing.T) {
		for _, p := range []string{"create", "update", "delete", "toggle", "regenerateSecret", "test"} {
			rec, reached := runAuthz(t, member, "POST", "/api/webhookSubscriptions."+p, `{"workspace_id":"ws"}`)
			assert.Equal(t, http.StatusForbidden, rec.Code, p)
			assert.False(t, *reached, p)
			rec, reached = runAuthz(t, owner, "POST", "/api/webhookSubscriptions."+p, `{"workspace_id":"ws"}`)
			assert.Equal(t, 200, rec.Code, p)
			assert.True(t, *reached, p)
		}
	})
	t.Run("lectures et segments : une cle scopee member reste servie", func(t *testing.T) {
		for _, c := range [][3]string{
			{"GET", "/api/webhookSubscriptions.list?workspace_id=ws", ""},
			{"POST", "/api/segments.create", `{"workspace_id":"ws","id":"s"}`},
			{"GET", "/api/timeline.list?workspace_id=ws", ""},
		} {
			rec, reached := runAuthz(t, member, c[0], c[1], c[2])
			assert.Equal(t, 200, rec.Code, c[1])
			assert.True(t, *reached, c[1])
		}
	})
	t.Run("le corps est relisible par le handler", func(t *testing.T) {
		var got string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			got = string(b)
		})
		h := VeridianWorkspaceAuthzMiddleware(owner, func() ([]byte, error) { return []byte("sec"), nil })(next)
		req := httptest.NewRequest("POST", "/api/segments.create", strings.NewReader(`{"workspace_id":"ws","x":1}`))
		req.Header.Set("Authorization", "Bearer "+authzToken(t, "user"))
		h.ServeHTTP(httptest.NewRecorder(), req)
		assert.Equal(t, `{"workspace_id":"ws","x":1}`, got)
	})
	t.Run("sans jeton 401, sans workspace_id 400, erreur interne 500", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("atteint") })
		h := VeridianWorkspaceAuthzMiddleware(owner, func() ([]byte, error) { return []byte("sec"), nil })(next)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/segments.list?workspace_id=ws", nil))
		assert.Equal(t, 401, rec.Code)
		rec, _ = runAuthz(t, owner, "GET", "/api/segments.list", "")
		assert.Equal(t, 400, rec.Code)
		rec, _ = runAuthz(t, &fakeAuthz{err: errors.New("db down")}, "GET", "/api/segments.list?workspace_id=ws", "")
		assert.Equal(t, 500, rec.Code)
	})
	t.Run("routes hors table inchangees", func(t *testing.T) {
		rec, reached := runAuthz(t, foreign, "GET", "/api/contacts.list?workspace_id=ws", "")
		assert.Equal(t, 200, rec.Code)
		assert.True(t, *reached)
	})
}

// Chaque route de la table existe vraiment dans le mux : un renommage amont
// rendrait la garde muette sans ce test.
func TestVeridianWorkspaceAuthzRoutesAreMounted(t *testing.T) {
	mux := http.NewServeMux()
	secret := func() ([]byte, error) { return []byte("sec"), nil }
	NewWebhookSubscriptionHandler(nil, nil, secret, nil).RegisterRoutes(mux)
	NewSegmentHandler(nil, secret, nil).RegisterRoutes(mux)
	NewContactTimelineHandler(nil, nil, secret, nil).RegisterRoutes(mux)
	for route := range VeridianWorkspaceAuthzRoutes {
		_, pattern := mux.Handler(httptest.NewRequest("POST", route, nil))
		assert.Equal(t, route, pattern)
	}
}
