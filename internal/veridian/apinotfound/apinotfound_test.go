package apinotfound

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func get(mux http.Handler, method, path string) (int, map[string]string) {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	out := map[string]string{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestMount(t *testing.T) {
	mux := http.NewServeMux()
	root := 0
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { root++; w.WriteHeader(http.StatusTeapot) })
	mux.HandleFunc("/api/lists.list", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	mux.HandleFunc("POST /api/veridian/x/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	Mount(mux)

	for _, c := range [][2]string{{"GET", "/api/lists.zzz"}, {"POST", "/api/veridian/membres"}, {"GET", "/api/veridian/x/1"}, {"GET", "/api/health/x"}, {"POST", "/api/a/b/c"}} {
		st, out := get(mux, c[0], c[1])
		require.Equal(t, http.StatusNotFound, st, c)
		assert.Equal(t, Code, out["code"])
		assert.Contains(t, out["error"], c[0]+" "+c[1])
	}
	for _, c := range [][2]string{{"GET", "/api/lists.list"}, {"DELETE", "/api/lists.list"}, {"POST", "/api/veridian/x/1"}} {
		st, _ := get(mux, c[0], c[1])
		assert.Equal(t, http.StatusAccepted, st, "une route réelle n'est jamais masquée", c)
	}
	st, out := get(mux, "GET", "/api/")
	assert.Equal(t, 200, st)
	assert.Equal(t, "api running", out["status"])
	st, out = get(mux, "GET", "/api/health")
	assert.Equal(t, 200, st, "des sondes la lisent")
	assert.Equal(t, "ok", out["status"])
	assert.Zero(t, root)
	for _, p := range []string{"/console", "/apiculture", "/"} {
		st, _ = get(mux, "GET", p)
		assert.Equal(t, http.StatusTeapot, st, "le reste ne change pas")
	}
}
