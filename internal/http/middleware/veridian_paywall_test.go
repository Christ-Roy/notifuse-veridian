package middleware

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPaywallReq(t *testing.T, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/transactional.send", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func nextHandler(called *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		// Lire le body pour verifier le rebuffering
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
}

func TestVeridianPaywall_ActivePlanLetsThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-1").Return(&domain.VeridianPlan{
		WorkspaceID:         "ws-1",
		Plan:                "pro",
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   10000,
		EmailsSentThisMonth: 100,
	}, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-1"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
}

func TestVeridianPaywall_SuspendedReturns402(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-2").Return(&domain.VeridianPlan{
		WorkspaceID:     "ws-2",
		Plan:            "pro",
		Status:          domain.PlanStatusSuspended,
		SuspendedReason: "non-payment",
	}, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-2"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.False(t, called.Load(), "next handler must NOT be called")
	assert.Contains(t, rec.Body.String(), "non-payment")
	assert.Contains(t, rec.Body.String(), "suspended")
}

func TestVeridianPaywall_QuotaExceededReturns402(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-3").Return(&domain.VeridianPlan{
		WorkspaceID:         "ws-3",
		Plan:                "free",
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   500,
		EmailsSentThisMonth: 500,
	}, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-3"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.False(t, called.Load())
	assert.Contains(t, rec.Body.String(), "quota")
}

func TestVeridianPaywall_DeletedReturns402(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	deletedAt := time.Now().Add(-1 * time.Hour)
	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-4").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-4",
		Plan:        "free",
		Status:      domain.PlanStatusDeleted,
		DeletedAt:   &deletedAt,
	}, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-4"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.False(t, called.Load())
	assert.Contains(t, rec.Body.String(), "deleted")
}

func TestVeridianPaywall_PlanAbsentLetsThrough(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-self-hosted").Return(nil, sql.ErrNoRows).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-self-hosted"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "self-hosted workspace must pass")
	assert.True(t, called.Load())
}

func TestVeridianPaywall_DBErrorFailsOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-5").Return(nil, errors.New("connection refused")).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-5"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "DB error must fail-open")
	assert.True(t, called.Load())
}

func TestVeridianPaywall_CacheHitAvoidsDBQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// EXPECTED: une seule fois (deuxieme appel = cache hit)
	repo.EXPECT().Get(gomock.Any(), "ws-cached").Return(&domain.VeridianPlan{
		WorkspaceID:       "ws-cached",
		Plan:              "pro",
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: 10000,
	}, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	for i := 0; i < 3; i++ {
		req := newPaywallReq(t, `{"workspace_id":"ws-cached"}`)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}
	assert.True(t, called.Load())
	// Les attentes gomock verifient implicitement que Get n'a ete appelle qu'une fois.
}

func TestVeridianPaywall_NilRepoIsPassthrough(t *testing.T) {
	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(nil, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-x"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
}

func TestVeridianPaywall_GETPasses(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// Aucun appel attendu

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/transactional.send", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
}

func TestVeridianPaywall_NoWorkspaceIDInBodyPasses(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"some_other_field":"value"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
}

func TestVeridianPaywall_BodyRebufferedForHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-rebuf").Return(nil, sql.ErrNoRows).Times(1)

	expectedBody := `{"workspace_id":"ws-rebuf","extra":"data with spaces"}`
	var gotBody string
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	req := newPaywallReq(t, expectedBody)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, expectedBody, gotBody, "next handler must see same body")
}

func TestVeridianPaywall_PathFilter(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// Get appele uniquement pour /api/transactional.send (path protege)
	repo.EXPECT().Get(gomock.Any(), "ws-path").Return(nil, sql.ErrNoRows).Times(1)

	var called atomic.Bool
	wrap := VeridianPaywallPathFilter(repo, logger.NewLogger())(nextHandler(&called))

	// Path non-protege : pas d'appel paywall
	req1 := httptest.NewRequest(http.MethodPost, "/api/users.list", strings.NewReader(`{"workspace_id":"ws-path"}`))
	rec1 := httptest.NewRecorder()
	wrap.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	// Path protege : paywall execute (ici on autorise via ErrNoRows)
	req2 := httptest.NewRequest(http.MethodPost, "/api/transactional.send", strings.NewReader(`{"workspace_id":"ws-path"}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	wrap.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

// === Veridian patch === Tests pour PaywallCache.Invalidate et Clear,
// ainsi que NewVeridianPaywallMiddlewareWithCache pour valider que le cache
// partage entre middleware et handler admin fonctionne sans race.

func TestVeridianPaywallCache_InvalidateForcesFreshLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// Premier call : plan suspended → 402 attendu
	repo.EXPECT().Get(gomock.Any(), "ws-inv").Return(&domain.VeridianPlan{
		WorkspaceID:     "ws-inv",
		Plan:            "pro",
		Status:          domain.PlanStatusSuspended,
		SuspendedReason: "test",
	}, nil).Times(1)
	// Apres Invalidate : second call DB obligatoire → on retourne plan active
	repo.EXPECT().Get(gomock.Any(), "ws-inv").Return(&domain.VeridianPlan{
		WorkspaceID:       "ws-inv",
		Plan:              "pro",
		Status:            domain.PlanStatusActive,
		MonthlyEmailQuota: 10000,
	}, nil).Times(1)

	cache := NewPaywallCache()
	var called atomic.Bool
	mw := NewVeridianPaywallMiddlewareWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	// Premier passage : suspended → 402 + cache hit pour les suivants (TTL 60s)
	req1 := newPaywallReq(t, `{"workspace_id":"ws-inv"}`)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusPaymentRequired, rec1.Code)

	// Deuxieme passage SANS Invalidate : cache hit, repo.Get pas re-appele
	req2 := newPaywallReq(t, `{"workspace_id":"ws-inv"}`)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusPaymentRequired, rec2.Code)

	// Invalidate → force fresh DB lookup au prochain passage
	cache.Invalidate("ws-inv")

	// Troisieme passage : repo.Get re-appele, retourne active → 200
	req3 := newPaywallReq(t, `{"workspace_id":"ws-inv"}`)
	rec3 := httptest.NewRecorder()
	mw.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code)
	assert.True(t, called.Load())
}

func TestVeridianPaywallCache_InvalidateIdempotentOnEmptyCache(t *testing.T) {
	cache := NewPaywallCache()
	// Aucune entree : Invalidate ne doit pas paniquer ni renvoyer d'erreur
	cache.Invalidate("ws-doesnotexist")
	cache.Invalidate("")
}

func TestVeridianPaywallCache_ClearRemovesAllEntries(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// 4 lookups DB attendus : 2 entrees, populées une fois, refresh apres Clear
	repo.EXPECT().Get(gomock.Any(), "ws-a").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-a", Plan: "free", Status: domain.PlanStatusActive, MonthlyEmailQuota: 500,
	}, nil).Times(2)
	repo.EXPECT().Get(gomock.Any(), "ws-b").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-b", Plan: "pro", Status: domain.PlanStatusActive, MonthlyEmailQuota: 10000,
	}, nil).Times(2)

	cache := NewPaywallCache()
	var called atomic.Bool
	mw := NewVeridianPaywallMiddlewareWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	// Populate cache pour ws-a et ws-b
	for _, ws := range []string{"ws-a", "ws-b"} {
		req := newPaywallReq(t, `{"workspace_id":"`+ws+`"}`)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}

	// Clear : les deux entrees disparaissent
	cache.Clear()

	// Re-passage : repo.Get re-appele pour les deux (Times=2 dans expectations)
	for _, ws := range []string{"ws-a", "ws-b"} {
		req := newPaywallReq(t, `{"workspace_id":"`+ws+`"}`)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}
}

func TestVeridianPaywallCache_InvalidateAffectsOnlyOneEntry(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// ws-a : 2 lookups (initial + apres Invalidate), ws-b : 1 lookup uniquement (cache)
	repo.EXPECT().Get(gomock.Any(), "ws-a").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-a", Plan: "free", Status: domain.PlanStatusActive, MonthlyEmailQuota: 500,
	}, nil).Times(2)
	repo.EXPECT().Get(gomock.Any(), "ws-b").Return(&domain.VeridianPlan{
		WorkspaceID: "ws-b", Plan: "pro", Status: domain.PlanStatusActive, MonthlyEmailQuota: 10000,
	}, nil).Times(1)

	cache := NewPaywallCache()
	var called atomic.Bool
	mw := NewVeridianPaywallMiddlewareWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	// Populate les deux
	for _, ws := range []string{"ws-a", "ws-b"} {
		req := newPaywallReq(t, `{"workspace_id":"`+ws+`"}`)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
	}

	// Invalidate uniquement ws-a
	cache.Invalidate("ws-a")

	// Re-passage des deux : ws-a refresh, ws-b cache hit
	for _, ws := range []string{"ws-a", "ws-b"} {
		req := newPaywallReq(t, `{"workspace_id":"`+ws+`"}`)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	}
}

func TestVeridianPaywallCache_InvalidateNotFoundEntry(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// 2 lookups : initial (ErrNoRows) + apres Invalidate (refresh)
	repo.EXPECT().Get(gomock.Any(), "ws-nf").Return(nil, sql.ErrNoRows).Times(2)

	cache := NewPaywallCache()
	var called atomic.Bool
	mw := NewVeridianPaywallMiddlewareWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	// Initial : ErrNoRows mis en cache
	req1 := newPaywallReq(t, `{"workspace_id":"ws-nf"}`)
	rec1 := httptest.NewRecorder()
	mw.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code) // notFound = passthrough

	// Cache hit (pas de DB call) : 2eme passage
	req2 := newPaywallReq(t, `{"workspace_id":"ws-nf"}`)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusOK, rec2.Code)

	// Invalidate puis 3eme passage : DB ré-appelé (Times=2 satisfait)
	cache.Invalidate("ws-nf")
	req3 := newPaywallReq(t, `{"workspace_id":"ws-nf"}`)
	rec3 := httptest.NewRecorder()
	mw.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code)
}

func TestVeridianNewPaywallCache_ReturnsEmptyCache(t *testing.T) {
	cache := NewPaywallCache()
	require.NotNil(t, cache)
	// Cache vide : get retourne false
	_, hit := cache.get("ws-x")
	assert.False(t, hit)
}

// Concurrence : Invalidate et lookups depuis plusieurs goroutines sans race
// (sync.Map est thread-safe par contrat — ce test verifie qu'on n'a pas
// introduit de race autour).
func TestVeridianPaywallCache_ConcurrentInvalidateAndLookup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&domain.VeridianPlan{
		WorkspaceID: "ws-conc", Plan: "pro", Status: domain.PlanStatusActive, MonthlyEmailQuota: 10000,
	}, nil).AnyTimes()

	cache := NewPaywallCache()
	var called atomic.Bool
	mw := NewVeridianPaywallMiddlewareWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	done := make(chan struct{})
	const N = 50

	// Goroutine 1 : invalidate en boucle
	go func() {
		for i := 0; i < N; i++ {
			cache.Invalidate("ws-conc")
			time.Sleep(time.Microsecond * 100)
		}
		done <- struct{}{}
	}()

	// Goroutine 2 : passage middleware en boucle
	go func() {
		for i := 0; i < N; i++ {
			req := newPaywallReq(t, `{"workspace_id":"ws-conc"}`)
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, req)
		}
		done <- struct{}{}
	}()

	<-done
	<-done
}
