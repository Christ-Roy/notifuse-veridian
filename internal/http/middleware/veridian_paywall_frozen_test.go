package middleware

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
// Tests middleware VeridianFrozenMemberFilter / NewVeridianFrozenMemberMiddleware.
//
// Couvre :
//   - passthrough quand frozenRepo nil (mode self-hosted)
//   - passthrough quand getJWTSecret nil
//   - passthrough sur paths exempts (/api/veridian/, /api/tenants/, /api/health, /api/version, /api/auth/)
//   - passthrough quand pas de JWT (route publique ou anonyme)
//   - passthrough quand JWT valide mais workspace_id introuvable
//   - passthrough quand user pas frozen
//   - 402 user_frozen sur writes quand user frozen
//   - reads obfusques sur GET quand user frozen
//   - cache hit (pas de 2eme call repo)
//   - FrozenMemberCache : get/set/Invalidate/Clear

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper : handler upstream JSON simple.
func frozenTestHandler(called *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if called != nil {
			called.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"msg": "hello", "email": "user@x.test"})
	})
}

// fakeSecret est le secret HMAC utilise pour signer les JWTs de test.
var fakeSecret = []byte("test-secret-for-jwt-signing-only")

// signTestJWT genere un JWT signe avec user_id dans les claims.
func signTestJWT(t *testing.T, userID string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(1 * time.Hour).Unix(),
	})
	signed, err := token.SignedString(fakeSecret)
	require.NoError(t, err)
	return signed
}

func getJWTSecretFn() func() ([]byte, error) {
	return func() ([]byte, error) { return fakeSecret, nil }
}

// === Passthrough scenarios ==================================================

func TestVeridianFrozenMiddleware_NoRepo_Passthrough(t *testing.T) {
	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(nil, getJWTSecretFn(), nil)(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-1", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load(), "handler must be called when repo nil")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianFrozenMiddleware_NoJWTSecretFn_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, nil, nil)(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-1", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load())
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianFrozenMiddleware_ExemptedPath_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	// Pas d'EXPECT().IsFrozen — la route est exempted.

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), nil)(frozenTestHandler(called))

	for _, path := range []string{
		"/api/veridian/admin/cache/invalidate",
		"/api/tenants/provision",
		"/api/health",
		"/api/version",
		"/api/auth/signin",
	} {
		called.Store(false)
		req := httptest.NewRequest(http.MethodPost, path+"?workspace_id=ws-1", nil)
		req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.True(t, called.Load(), "exempted path %s must passthrough", path)
		assert.Equal(t, http.StatusOK, rec.Code)
	}
}

func TestVeridianFrozenMiddleware_NoJWT_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	// Pas d'EXPECT — extractUserIDFromJWT echoue silencieusement.

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), nil)(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-1", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load())
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianFrozenMiddleware_NoWorkspaceID_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	// Pas d'EXPECT — workspace_id absent → passe.

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), nil)(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/some-route", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load())
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianFrozenMiddleware_NotFrozen_Passthrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	repo.EXPECT().IsFrozen(gomock.Any(), "ws-1", "user-1").Return(false, domain.FreezeReason(""), nil)

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), nil)(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-1", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load())
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianFrozenMiddleware_RepoError_FailOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	repo.EXPECT().IsFrozen(gomock.Any(), "ws-1", "user-1").Return(false, domain.FreezeReason(""), errors.New("conn dead"))

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), logger.NewLogger())(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-1", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load(), "DB error → fail-open passthrough")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// === Frozen scenarios =======================================================

func TestVeridianFrozenMiddleware_Frozen_Write_Returns402(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	repo.EXPECT().IsFrozen(gomock.Any(), "ws-1", "user-1").
		Return(true, domain.FreezeReasonQuotaSeatExceeded, nil)

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), nil)(frozenTestHandler(called))

	body := strings.NewReader(`{"workspace_id":"ws-1","name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/contacts.create", body)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.False(t, called.Load(), "handler must NOT be called for frozen user on write")
	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	var respBody map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &respBody))
	assert.Equal(t, "user_frozen", respBody["code"])
	assert.Equal(t, "user_frozen", respBody["error"])
	assert.Equal(t, "quota_seat_exceeded", respBody["reason"])
	assert.Contains(t, respBody["unfreeze_url"], "ws-1")
}

func TestVeridianFrozenMiddleware_Frozen_Read_Obfuscated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	repo.EXPECT().IsFrozen(gomock.Any(), "ws-1", "user-1").
		Return(true, domain.FreezeReasonManual, nil)

	called := &atomic.Bool{}
	mw := NewVeridianFrozenMemberMiddleware(repo, getJWTSecretFn(), nil)(frozenTestHandler(called))

	req := httptest.NewRequest(http.MethodGet, "/api/contacts.list?workspace_id=ws-1", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called.Load(), "handler IS called on reads (we capture + obfuscate)")
	assert.Equal(t, http.StatusOK, rec.Code, "obfuscated response stays 200 OK")

	// Headers UI : X-User-Frozen=true et reason expose.
	assert.Equal(t, "true", rec.Header().Get("X-User-Frozen"))
	assert.Equal(t, "manual", rec.Header().Get("X-User-Frozen-Reason"))

	// La response est JSON et a ete obfuscee (le contenu n'est plus litteralement "hello").
	body := rec.Body.String()
	assert.Contains(t, body, "msg", "JSON structure preserved")
	// L'email original ne doit pas etre lisible (champ sensible).
	assert.NotContains(t, body, "user@x.test", "sensitive field email must be obfuscated")
}

func TestVeridianFrozenMiddleware_Frozen_CacheHit_AvoidsSecondRepoCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)
	// 1 seul EXPECT pour 2 calls : le 2eme doit hit le cache.
	repo.EXPECT().IsFrozen(gomock.Any(), "ws-1", "user-1").
		Return(true, domain.FreezeReasonManual, nil).Times(1)

	cache := NewFrozenMemberCache()
	mw := NewVeridianFrozenMemberMiddlewareWithCache(cache, repo, getJWTSecretFn(), nil)(frozenTestHandler(nil))

	for i := 0; i < 2; i++ {
		body := strings.NewReader(`{"workspace_id":"ws-1"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/contacts.create", body)
		req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusPaymentRequired, rec.Code, "call #%d", i+1)
	}
}

// === FrozenMemberCache helpers ==============================================

func TestFrozenMemberCache_GetSet(t *testing.T) {
	c := NewFrozenMemberCache()
	entry := frozenCacheEntry{frozen: true, reason: domain.FreezeReasonManual, expiresAt: time.Now().Add(1 * time.Minute)}
	c.set("ws-1", "u-1", entry)
	got, hit := c.get("ws-1", "u-1")
	assert.True(t, hit)
	assert.True(t, got.frozen)
}

func TestFrozenMemberCache_Expired_Evicted(t *testing.T) {
	c := NewFrozenMemberCache()
	entry := frozenCacheEntry{frozen: true, expiresAt: time.Now().Add(-1 * time.Minute)}
	c.set("ws-1", "u-1", entry)
	_, hit := c.get("ws-1", "u-1")
	assert.False(t, hit, "expired entry must be evicted on get")
}

func TestFrozenMemberCache_Invalidate(t *testing.T) {
	c := NewFrozenMemberCache()
	c.set("ws-1", "u-1", frozenCacheEntry{frozen: true, expiresAt: time.Now().Add(1 * time.Minute)})
	c.Invalidate("ws-1", "u-1")
	_, hit := c.get("ws-1", "u-1")
	assert.False(t, hit)
}

func TestFrozenMemberCache_Invalidate_IdempotentOnMiss(t *testing.T) {
	c := NewFrozenMemberCache()
	// Pas d'erreur sur cle inexistante.
	c.Invalidate("ws-unknown", "u-unknown")
}

func TestFrozenMemberCache_Clear(t *testing.T) {
	c := NewFrozenMemberCache()
	c.set("ws-1", "u-1", frozenCacheEntry{frozen: true, expiresAt: time.Now().Add(1 * time.Minute)})
	c.set("ws-2", "u-2", frozenCacheEntry{frozen: true, expiresAt: time.Now().Add(1 * time.Minute)})
	c.Clear()
	_, hit := c.get("ws-1", "u-1")
	assert.False(t, hit)
	_, hit = c.get("ws-2", "u-2")
	assert.False(t, hit)
}

// === extractUserIDFromJWT helpers ===========================================

func TestExtractUserIDFromJWT_Valid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-42"))

	uid, ok := extractUserIDFromJWT(req, getJWTSecretFn())
	assert.True(t, ok)
	assert.Equal(t, "user-42", uid)
}

func TestExtractUserIDFromJWT_MissingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	uid, ok := extractUserIDFromJWT(req, getJWTSecretFn())
	assert.False(t, ok)
	assert.Empty(t, uid)
}

func TestExtractUserIDFromJWT_BadFormat(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "NotBearer xxx")
	uid, ok := extractUserIDFromJWT(req, getJWTSecretFn())
	assert.False(t, ok)
	assert.Empty(t, uid)
}

func TestExtractUserIDFromJWT_SecretError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))

	uid, ok := extractUserIDFromJWT(req, func() ([]byte, error) {
		return nil, fmt.Errorf("secret unavailable")
	})
	assert.False(t, ok)
	assert.Empty(t, uid)
}

func TestExtractUserIDFromJWT_InvalidSignature(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, "user-1"))

	// Different secret → invalid signature.
	uid, ok := extractUserIDFromJWT(req, func() ([]byte, error) {
		return []byte("different-secret-32-bytes-yepyep"), nil
	})
	assert.False(t, ok)
	assert.Empty(t, uid)
}

func TestExtractUserIDFromJWT_MissingUserIDClaim(t *testing.T) {
	// JWT signe mais sans user_id claim.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"other": "claim",
		"exp":   time.Now().Add(1 * time.Hour).Unix(),
	})
	signed, err := token.SignedString(fakeSecret)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	uid, ok := extractUserIDFromJWT(req, getJWTSecretFn())
	assert.False(t, ok)
	assert.Empty(t, uid)
}

// === Sanity : Filter wrappers re-export ====================================

func TestVeridianFrozenMemberFilter_Wraps(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)

	mw := VeridianFrozenMemberFilter(repo, getJWTSecretFn(), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVeridianFrozenMemberFilterWithCache_Wraps(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianFrozenMemberRepository(ctrl)

	cache := NewFrozenMemberCache()
	mw := VeridianFrozenMemberFilterWithCache(cache, repo, getJWTSecretFn(), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
