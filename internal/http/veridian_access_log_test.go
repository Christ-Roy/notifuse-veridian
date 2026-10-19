package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

// runAccessLog rejoue une requete dans le middleware et renvoie les champs
// journalises (nil si rien n'a ete journalise).
func runAccessLog(t *testing.T, method, target, body, auth string, status int, lookup VeridianTokenWorkspaceLookup, extra map[string]string) map[string]interface{} {
	ctrl := gomock.NewController(t)
	log := pkgmocks.NewMockLogger(ctrl)
	var got map[string]interface{}
	infos := 0
	log.EXPECT().WithFields(gomock.Any()).DoAndReturn(func(f map[string]interface{}) *pkgmocks.MockLogger { got = f; return log }).AnyTimes()
	log.EXPECT().Info(gomock.Any()).Do(func(string) { infos++ }).AnyTimes()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	VeridianAccessLogMiddleware(log, lookup)(next).ServeHTTP(httptest.NewRecorder(), req)
	if got != nil {
		require.Equal(t, 1, infos)
	}
	return got
}

func TestVeridianAccessLog(t *testing.T) {
	apiKey := "Bearer " + authzToken(t, "api_key")

	t.Run("get : workspace de la query, cle API, workspace du jeton, aucun secret", func(t *testing.T) {
		f := runAccessLog(t, "GET", "/api/webhookSubscriptions.list?workspace_id=victim&email=secret@x.fr&token=zzz", "", apiKey, 403,
			func(context.Context, string) ([]string, error) { return []string{"mine"}, nil }, nil)
		require.NotNil(t, f)
		assert.Equal(t, "/api/webhookSubscriptions.list", f["route"])
		assert.Equal(t, "victim", f["workspace_req"])
		assert.Equal(t, "api_key", f["token_type"])
		assert.Equal(t, "mine", f["token_workspace"])
		assert.Equal(t, 403, f["status"])
		for k, v := range f {
			s, _ := v.(string)
			assert.NotContains(t, s, "secret@x.fr", k)
			assert.NotContains(t, s, "zzz", k)
			assert.NotContains(t, s, "eyJ", k)
		}
	})

	t.Run("post : workspace_id du corps, corps relisible, contenu jamais journalise", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		log := pkgmocks.NewMockLogger(ctrl)
		var got map[string]interface{}
		log.EXPECT().WithFields(gomock.Any()).DoAndReturn(func(f map[string]interface{}) *pkgmocks.MockLogger { got = f; return log })
		log.EXPECT().Info(gomock.Any())
		var seen string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			seen = string(b)
		})
		body := `{"workspace_id":"ws9","password":"hunter2"}`
		req := httptest.NewRequest("POST", "/api/segments.rebuild", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+authzToken(t, "user"))
		VeridianAccessLogMiddleware(log, nil)(next).ServeHTTP(httptest.NewRecorder(), req)
		assert.Equal(t, body, seen)
		assert.Equal(t, "ws9", got["workspace_req"])
		assert.Equal(t, "jwt_user", got["token_type"])
		for k, v := range got {
			s, _ := v.(string)
			assert.NotContains(t, s, "hunter2", k)
		}
	})

	t.Run("hmac, sans jeton ; hors /api et health ignores", func(t *testing.T) {
		f := runAccessLog(t, "POST", "/api/tenants/provision", "", "", 200, nil, map[string]string{"X-Veridian-Hub-Signature": "abc"})
		assert.Equal(t, "hmac", f["token_type"])
		f = runAccessLog(t, "GET", "/api/contacts.list", "", "", 401, nil, nil)
		assert.Equal(t, "none", f["token_type"])
		assert.Nil(t, runAccessLog(t, "GET", "/console/x.js", "", "", 200, nil, nil))
		assert.Nil(t, runAccessLog(t, "GET", "/api/health", "", "", 200, nil, nil))
	})
}

// Le statut journalise est celui reellement ecrit par le handler (WriteHeader).
func TestVeridianAccessLog_StatusIsTheWrittenOne(t *testing.T) {
	for _, code := range []int{200, 403, 502} {
		f := runAccessLog(t, "GET", "/api/segments.list?workspace_id=w", "", "", code, nil, nil)
		require.NotNil(t, f)
		assert.Equal(t, code, f["status"])
	}
}
