package middleware

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

func newIdempotencyTestHandler(body string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Lire le body pour s'assurer que le middleware a bien rebuffere.
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestVeridianIdempotencyMiddleware_NilRepoPassthrough(t *testing.T) {
	// repo nil = mode self-hosted sans tracking : passthrough complet.
	mw := VeridianIdempotencyMiddleware(nil, nil)
	handler := mw(newIdempotencyTestHandler(`{"ok":true}`, http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", bytes.NewReader([]byte(`{"tenant_id":"ws-1"}`)))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"ok":true}`, rec.Body.String())
	assert.Empty(t, rec.Header().Get("X-Idempotent-Replay"))
}

func TestVeridianIdempotencyMiddleware_NoHeaderPassthrough(t *testing.T) {
	// Header absent (Hub legacy) : passthrough sans tracking.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)
	// PAS d'attente sur repo : ne doit pas etre appele du tout.

	mw := VeridianIdempotencyMiddleware(repo, nil)
	handler := mw(newIdempotencyTestHandler(`{"ok":true}`, http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianIdempotencyMiddleware_MissExecutesAndSaves(t *testing.T) {
	// Premier passage : Get → ErrNoRows, handler s'execute, Save est appele.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	repo.EXPECT().Get(gomock.Any(), "k1").Return(nil, sql.ErrNoRows).Times(1)
	repo.EXPECT().Save(gomock.Any(), gomock.AssignableToTypeOf(&domain.VeridianIdempotencyEntry{})).
		DoAndReturn(func(_ context.Context, e *domain.VeridianIdempotencyEntry) error {
			assert.Equal(t, "k1", e.Key)
			assert.Equal(t, "/api/tenants/provision", e.Endpoint)
			assert.Equal(t, "ws-1", e.TenantID, "tenant_id extrait du body")
			assert.Equal(t, http.StatusOK, e.ResponseStatus)
			assert.JSONEq(t, `{"workspace_id":"ws-1"}`, string(e.ResponseBody))
			// ExpiresAt = NOW + 24h
			assert.WithinDuration(t, time.Now().Add(24*time.Hour), e.ExpiresAt, 5*time.Second)
			return nil
		}).Times(1)

	mw := VeridianIdempotencyMiddleware(repo, nil)
	handler := mw(newIdempotencyTestHandler(`{"workspace_id":"ws-1"}`, http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision",
		bytes.NewReader([]byte(`{"tenant_id":"ws-1","owner_email":"o@x"}`)))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"workspace_id":"ws-1"}`, rec.Body.String())
}

func TestVeridianIdempotencyMiddleware_HitReplaysCached(t *testing.T) {
	// Deuxieme passage : Get retourne l'entry → replay, handler PAS execute.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	cachedBody := []byte(`{"workspace_id":"ws-1","cached":true}`)
	requestBody := []byte(`{"tenant_id":"ws-1"}`)
	hash := hashRequest("POST", "/api/tenants/provision", requestBody)

	repo.EXPECT().Get(gomock.Any(), "k1").Return(&domain.VeridianIdempotencyEntry{
		Key:            "k1",
		Endpoint:       "/api/tenants/provision",
		TenantID:       "ws-1",
		RequestHash:    hash,
		ResponseStatus: http.StatusOK,
		ResponseBody:   cachedBody,
	}, nil).Times(1)
	// PAS d'appel Save attendu : c'est un replay, rien a re-stocker.

	mw := VeridianIdempotencyMiddleware(repo, nil)
	// Handler "qui ne devrait pas tourner" : si appele, on retourne 500 pour fail le test.
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should NOT be called on cache hit")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision", bytes.NewReader(requestBody))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"workspace_id":"ws-1","cached":true}`, rec.Body.String())
	assert.Equal(t, "true", rec.Header().Get("X-Idempotent-Replay"))
}

func TestVeridianIdempotencyMiddleware_HashMismatchReturns422(t *testing.T) {
	// Meme cle mais body different : 422 idempotency_key_mismatch.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	repo.EXPECT().Get(gomock.Any(), "k1").Return(&domain.VeridianIdempotencyEntry{
		Key:            "k1",
		Endpoint:       "/api/tenants/provision",
		RequestHash:    "different_hash",
		ResponseStatus: http.StatusOK,
		ResponseBody:   []byte(`{"workspace_id":"ws-1"}`),
	}, nil).Times(1)

	mw := VeridianIdempotencyMiddleware(repo, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should NOT be called on hash mismatch")
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision",
		bytes.NewReader([]byte(`{"tenant_id":"ws-2"}`)))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "idempotency_key_mismatch", resp["code"])
}

func TestVeridianIdempotencyMiddleware_DBFailureFailOpen(t *testing.T) {
	// Erreur DB non-ErrNoRows sur Get : fail-open, laisser passer le handler
	// sans tracker l'idempotence (alternative fail-closed bloquerait tout).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	repo.EXPECT().Get(gomock.Any(), "k1").Return(nil, errors.New("db down")).Times(1)
	// PAS de Save attendu.

	mw := VeridianIdempotencyMiddleware(repo, nil)
	handler := mw(newIdempotencyTestHandler(`{"ok":true}`, http.StatusOK))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision",
		bytes.NewReader([]byte(`{"tenant_id":"ws-1"}`)))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "fail-open : handler s'execute malgre l'erreur DB")
}

func TestVeridianIdempotencyMiddleware_5xxNotCached(t *testing.T) {
	// 500 = incident transitoire, on ne cache pas (le client doit pouvoir
	// retry et obtenir un succes).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	repo.EXPECT().Get(gomock.Any(), "k1").Return(nil, sql.ErrNoRows).Times(1)
	// PAS de Save attendu : 500 n'est pas cachee.

	mw := VeridianIdempotencyMiddleware(repo, nil)
	handler := mw(newIdempotencyTestHandler(`{"error":"boom"}`, http.StatusInternalServerError))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision",
		bytes.NewReader([]byte(`{"tenant_id":"ws-1"}`)))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVeridianIdempotencyMiddleware_4xxIsCached(t *testing.T) {
	// 4xx (validation, conflit metier) = reponse stable : cachee pour eviter
	// que le client retry et obtienne une reponse differente apres mutation DB.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianIdempotencyRepository(ctrl)

	repo.EXPECT().Get(gomock.Any(), "k1").Return(nil, sql.ErrNoRows).Times(1)
	repo.EXPECT().Save(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	mw := VeridianIdempotencyMiddleware(repo, nil)
	handler := mw(newIdempotencyTestHandler(`{"error":"invalid_payload"}`, http.StatusBadRequest))

	req := httptest.NewRequest(http.MethodPost, "/api/tenants/provision",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHashRequest_DifferentMethodOrPathYieldDifferentHashes(t *testing.T) {
	body := []byte(`{"x":1}`)
	h1 := hashRequest("POST", "/api/tenants/provision", body)
	h2 := hashRequest("POST", "/api/tenants/update-plan", body)
	h3 := hashRequest("DELETE", "/api/tenants/provision", body)
	h4 := hashRequest("POST", "/api/tenants/provision", []byte(`{"x":2}`))

	assert.NotEqual(t, h1, h2, "path different = hash different")
	assert.NotEqual(t, h1, h3, "method different = hash different")
	assert.NotEqual(t, h1, h4, "body different = hash different")
	// Sanity : meme inputs = meme output
	assert.Equal(t, h1, hashRequest("POST", "/api/tenants/provision", body))
}

func TestExtractTenantIDFromBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"present", `{"tenant_id":"ws-1","other":"x"}`, "ws-1"},
		{"absent", `{"other":"x"}`, ""},
		{"empty body", ``, ""},
		{"invalid JSON", `not json`, ""},
		{"null tenant_id", `{"tenant_id":null}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractTenantIDFromBody([]byte(tc.body)))
		})
	}
}

func TestShouldCacheResponse(t *testing.T) {
	cases := []struct {
		status int
		cache  bool
	}{
		{200, true},
		{201, true},
		{204, true},
		{400, true},
		{404, true},
		{409, true},
		{422, false}, // idempotency_key_mismatch lui-meme : pas de cache
		{500, false},
		{502, false},
		{100, false}, // informational
	}
	for _, tc := range cases {
		assert.Equal(t, tc.cache, shouldCacheResponse(tc.status), "status=%d", tc.status)
	}
}
