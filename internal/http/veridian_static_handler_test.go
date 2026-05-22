package http

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nextHandlerSpy est un handler en aval qui renvoie un corps + un statut
// configurables, et enregistre s'il a ete appele.
func nextHandlerSpy(status int, contentType, body string) (http.Handler, *bool) {
	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return h, &called
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(b)))
	require.NoError(t, err)
	out, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(out)
}

func TestVeridianStaticAssetsFilter_GzipsHashedJSAsset(t *testing.T) {
	body := strings.Repeat("console-bundle-content;", 500)
	next, called := nextHandlerSpy(http.StatusOK, "application/javascript", body)
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	req := httptest.NewRequest(http.MethodGet, "/console/assets/index-B7KYMJSS.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	filter.ServeHTTP(rec, req)

	assert.True(t, *called, "le handler en aval doit avoir ete appele")
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"))
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "application/javascript", rec.Header().Get("Content-Type"))
	// Le corps recu doit etre du gzip qui se decompresse vers l'original.
	assert.Equal(t, body, gunzip(t, rec.Body.Bytes()))
	// La compression doit reellement reduire la taille.
	assert.Less(t, rec.Body.Len(), len(body), "le corps gzippe doit etre plus petit que le brut")
}

func TestVeridianStaticAssetsFilter_NoCacheHeaderOnUnhashedAsset(t *testing.T) {
	next, _ := nextHandlerSpy(http.StatusOK, "application/javascript", "x")
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	// Nom sans hash cache-bustable → pas de Cache-Control immutable.
	req := httptest.NewRequest(http.MethodGet, "/console/assets/config.js", nil)
	rec := httptest.NewRecorder()
	filter.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Cache-Control"),
		"un asset sans hash ne doit pas recevoir de Cache-Control immutable")
}

func TestVeridianStaticAssetsFilter_PassthroughNonConsolePath(t *testing.T) {
	next, called := nextHandlerSpy(http.StatusOK, "application/json", `{"ok":true}`)
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	// Une route API ne doit ni etre gzippee par ce filtre ni recevoir
	// de Cache-Control : elle passe direct.
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	filter.ServeHTTP(rec, req)

	assert.True(t, *called)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Empty(t, rec.Header().Get("Cache-Control"))
	assert.Equal(t, `{"ok":true}`, rec.Body.String())
}

func TestVeridianStaticAssetsFilter_NoGzipWithoutAcceptEncoding(t *testing.T) {
	body := "uncompressed-bundle"
	next, _ := nextHandlerSpy(http.StatusOK, "application/javascript", body)
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	// Client qui n'annonce PAS gzip → corps brut, mais Cache-Control
	// quand meme pose (l'asset est hashe).
	req := httptest.NewRequest(http.MethodGet, "/console/assets/index-AbCdEfGh.js", nil)
	rec := httptest.NewRecorder()
	filter.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, body, rec.Body.String())
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
}

func TestVeridianStaticAssetsFilter_SkipsCompressionForImages(t *testing.T) {
	// Un .png est deja compresse — le filtre ne doit pas le gzipper.
	next, _ := nextHandlerSpy(http.StatusOK, "image/png", "fake-png-bytes")
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	req := httptest.NewRequest(http.MethodGet, "/console/assets/logo-AbCdEfGh.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	filter.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Content-Encoding"),
		"un .png ne doit pas etre gzippe")
	assert.Equal(t, "fake-png-bytes", rec.Body.String())
}

func TestVeridianStaticAssetsFilter_RelaysNonOKResponseUncompressed(t *testing.T) {
	// Si le handler upstream renvoie un 404 (fallback SPA), on relaie
	// la reponse telle quelle sans compresser ni masquer le statut.
	next, _ := nextHandlerSpy(http.StatusNotFound, "text/plain", "not found")
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	req := httptest.NewRequest(http.MethodGet, "/console/assets/missing-AbCdEfGh.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	filter.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "not found", rec.Body.String())
}

func TestVeridianStaticAssetsFilter_CacheServesSecondHitFromMemory(t *testing.T) {
	body := strings.Repeat("cached-asset;", 200)
	callCount := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
	filter := VeridianStaticAssetsFilter(logger.NewLogger())(next)

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/console/assets/index-CACHE001.js", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		filter.ServeHTTP(rec, req)
		return rec
	}

	first := doReq()
	second := doReq()

	// Le 2e hit doit venir du cache memoire : le handler upstream n'est
	// appele qu'une fois.
	assert.Equal(t, 1, callCount, "le handler upstream ne doit etre appele qu'une fois (cache)")
	assert.Equal(t, "gzip", second.Header().Get("Content-Encoding"))
	assert.Equal(t, body, gunzip(t, second.Body.Bytes()))
	assert.Equal(t, first.Body.Bytes(), second.Body.Bytes(),
		"les deux reponses gzippees doivent etre identiques")
}

func TestVeridianExtOf(t *testing.T) {
	cases := map[string]string{
		"/console/assets/index-AbCdEfGh.js": ".js",
		"/console/assets/style.CSS":         ".css",
		"/console/assets/logo.PNG":          ".png",
		"/console/assets/noext":             "",
	}
	for path, want := range cases {
		assert.Equal(t, want, veridianExtOf(path), "veridianExtOf(%q)", path)
	}
}

func TestVeridianHashedAssetRe(t *testing.T) {
	hashed := []string{
		"/console/assets/index-B7KYMJSS.js",
		"/console/assets/index-BdqHGIYi.css",
		"/console/assets/fr-D09p1dB3.js",
	}
	for _, p := range hashed {
		assert.True(t, veridianHashedAssetRe.MatchString(p), "%q doit matcher (hashe)", p)
	}
	notHashed := []string{
		"/console/assets/config.js",
		"/console/assets/short-Ab.js",
		"/console/assets/logo.png",
	}
	for _, p := range notHashed {
		assert.False(t, veridianHashedAssetRe.MatchString(p), "%q ne doit PAS matcher", p)
	}
}
