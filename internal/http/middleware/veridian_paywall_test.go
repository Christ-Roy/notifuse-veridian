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
