package middleware

import (
	"database/sql"
	"encoding/json"
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

// TestVeridianPaywall_QuotaDoesNotBlock vérifie qu'un tenant actif avec son
// compteur emails au-dessus du quota mensuel NE DOIT PLUS être bloqué par
// le paywall — décision 2026-05-20 (BYO sending : Veridian ne fournit aucun
// provider d'envoi, c'est le provider du client qui limite).
//
// Renomme l'ancien TestVeridianPaywall_QuotaExceededReturns402 qui validait
// l'ancien comportement. Le test sert maintenant de garde-fou anti-régression :
// si quelqu'un re-active le check quota dans IsBlocked(), ce test fail.
func TestVeridianPaywall_QuotaDoesNotBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-3").Return(&domain.VeridianPlan{
		WorkspaceID:         "ws-3",
		Plan:                "free",
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   500,
		EmailsSentThisMonth: 500, // au-dessus du quota
	}, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-3"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// 2026-05-20 : quota dépassé ne doit PLUS bloquer (BYO sending)
	assert.NotEqual(t, http.StatusPaymentRequired, rec.Code, "quota dépassé ne doit pas retourner 402")
	assert.True(t, called.Load(), "next handler doit être appelé (passthrough)")
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

// TestVeridianPaywall_DeletedReturnsSoftDeletedBodyFormat valide que la
// branche `IsBlocked.DeletedAt != nil` du middleware paywall (4 paths
// d'envoi) délègue maintenant à `writeSoftDeletedResponse` — donc même
// format que le middleware soft-deleted global (Lot J).
//
// Cohérence cross-route : un tenant soft-deleted qui envoie un email
// (paywall path) ET un client soft-deleted qui list ses contacts
// (middleware global) reçoivent EXACTEMENT le même body 402
// {error: tenant_soft_deleted, error_code, restore_url, deleted_at,
// purge_eligible_at}. La console peut afficher un message uniforme.
func TestVeridianPaywall_DeletedReturnsSoftDeletedBodyFormat(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	deletedAt := time.Now().Add(-1 * time.Hour)
	purgeAt := time.Now().Add(29 * 24 * time.Hour)
	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-softdel-paywall").Return(&domain.VeridianPlan{
		WorkspaceID:     "ws-softdel-paywall",
		Plan:            "pro",
		Status:          domain.PlanStatusDeleted,
		DeletedAt:       &deletedAt,
		PurgeEligibleAt: &purgeAt,
	}, nil).Times(1)

	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream NE DOIT PAS être appelé pour un tenant soft-deleted")
	}))

	req := newPaywallReq(t, `{"workspace_id":"ws-softdel-paywall"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	require.Equal(t, http.StatusPaymentRequired, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "tenant_soft_deleted", body["error"], "spec: error == tenant_soft_deleted")
	assert.Equal(t, "tenant_soft_deleted", body["error_code"], "spec: error_code machine-readable")
	assert.Contains(t, body["restore_url"], "/dashboard?action=restore&tenant=ws-softdel-paywall",
		"spec: restore_url pointe vers Hub avec tenant id")
	assert.NotEmpty(t, body["deleted_at"], "spec: deleted_at ISO 8601")
	assert.NotEmpty(t, body["purge_eligible_at"], "spec: purge_eligible_at ISO 8601")
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

// === V37 lot 4a feature gate A/B testing — REVERT 2026-05-21 ===
//
// Le pivot pricing 2026-05-21 a libere A/B testing pour tous les plans
// (cf. CLAUDE.md Notifuse §Vision pricing). La map featureGatedPaths
// est desormais vide → le middleware feature gate est effectivement
// desactive (pass-through systematique).
//
// Les tests ci-dessous verifient que :
//   1. La map featureGatedPaths reste vide (garde-fou anti-regression
//      si un agent re-ajoute un path par erreur)
//   2. Le helper checkFeatureAllowed reste fonctionnel (utilise par
//      l'UI pour decider d'afficher / griser un bouton sans bloquer)
//   3. Le middleware passe meme sur les paths historiquement gates

// newFeatureGateReq construit une requete sur un path historiquement
// feature-gated avec body JSON contenant workspace_id. Utilise pour
// verifier que le pass-through fonctionne post-pivot.
func newFeatureGateReq(t *testing.T, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// TestFeatureGatedPaths_EmptyAfterPivot — garde-fou pivot 2026-05-21.
// Si un agent re-ajoute une cle dans featureGatedPaths sans validation
// Robert, ce test casse. Maintient l'invariant "generosite maximale".
func TestFeatureGatedPaths_EmptyAfterPivot(t *testing.T) {
	assert.Empty(t, featureGatedPaths,
		"pivot 2026-05-21 : aucune feature ne doit etre gatee (A/B gratuit pour tous, etc.). "+
			"Re-ajouter une cle exige validation Robert + update CLAUDE.md.")
}

// TestVeridianFeatureGate_PathFilterPassesAllPaths — apres le pivot,
// les endpoints historiquement gates (A/B testing) passent au handler
// upstream sans intervention du feature gate.
func TestVeridianFeatureGate_PathFilterPassesAllPaths(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Le repo n'est meme pas appele : path filter ne route plus vers
	// le feature gate (map vide → isGated=false).
	repo := mocks.NewMockVeridianPlanRepository(ctrl)

	var called atomic.Bool
	cache := NewPaywallCache()
	filter := VeridianPaywallPathFilterWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	// Tester sur les anciens paths gates A/B
	for _, path := range []string{"/api/broadcasts.getTestResults", "/api/broadcasts.selectWinner"} {
		t.Run(path, func(t *testing.T) {
			called.Store(false)
			req := newFeatureGateReq(t, path, `{"workspace_id":"ws-any"}`)
			rec := httptest.NewRecorder()
			filter.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code, "pivot : A/B endpoints accessibles a tous")
			assert.True(t, called.Load(), "handler upstream doit etre atteint")
		})
	}
}

// TestVeridianFeatureGate_MiddlewareDirectStillFunctional — si on appelle
// le middleware feature gate directement (sans passer par le path filter)
// sur un path PAS dans la map, il doit laisser passer immediatement.
// Comportement intrinseque qui doit survivre meme apres re-ajout futur
// d'un path gate.
func TestVeridianFeatureGate_MiddlewareDirectStillFunctional(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	// Aucun appel sur le repo attendu

	var called atomic.Bool
	cache := NewPaywallCache()
	mw := NewVeridianFeatureGateMiddlewareWithCache(cache, repo, logger.NewLogger())(nextHandler(&called))

	// Un path qui n'est PAS dans featureGatedPaths (et meme si la map
	// est vide, ca couvre le cas "path inconnu" qui doit toujours passer).
	req := newFeatureGateReq(t, "/api/broadcasts.getTestResults", `{"workspace_id":"ws-any"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load())
}

// TestCheckFeatureAllowed_NilPlanReturnsTrue — invariant safe : si plan est
// nil (tenant non-Veridian), on autorise (fail-open).
func TestCheckFeatureAllowed_NilPlanReturnsTrue(t *testing.T) {
	assert.True(t, checkFeatureAllowed(nil, "ab_testing"))
	assert.True(t, checkFeatureAllowed(nil, "white_label"))
}

// TestCheckFeatureAllowed_UnknownFeatureFailsOpen — feature key inconnue
// (typo ou ajout future non-cable) → fail-open. Mieux qu'un panic.
func TestCheckFeatureAllowed_UnknownFeatureFailsOpen(t *testing.T) {
	p := &domain.VeridianPlan{Plan: "free"}
	assert.True(t, checkFeatureAllowed(p, "feature_from_the_future"))
}

// TestCheckFeatureAllowed_KnownFeatures verifie le mapping correct des 3
// features (ab_testing / branding_removed / white_label) vers les champs
// PlanLimits de la struct VeridianPlan. Le helper reste utile pour l'UI
// console qui peut decider d'afficher / griser des elements selon le
// plan, MEME sans enforcement backend (cf. pivot 2026-05-21 : pas de
// menu grisé "🔒 Pro", mais le helper sert si on veut un jour la
// telemetrie ou un toggle UI subtil).
func TestCheckFeatureAllowed_KnownFeatures(t *testing.T) {
	pro := &domain.VeridianPlan{
		Plan:                   "pro",
		FeatureABTesting:       true,
		FeatureBrandingRemoved: true,
		FeatureWhiteLabel:      false,
	}
	assert.True(t, checkFeatureAllowed(pro, "ab_testing"))
	assert.True(t, checkFeatureAllowed(pro, "branding_removed"))
	assert.False(t, checkFeatureAllowed(pro, "white_label"))

	// Apres le pivot, un Free a TOUS les features true sauf white_label.
	free := &domain.VeridianPlan{
		Plan:                   "free",
		FeatureABTesting:       true, // pivot : A/B gratuit
		FeatureBrandingRemoved: true, // pivot : branding optionnel
		FeatureWhiteLabel:      false,
	}
	assert.True(t, checkFeatureAllowed(free, "ab_testing"))
	assert.True(t, checkFeatureAllowed(free, "branding_removed"))
	assert.False(t, checkFeatureAllowed(free, "white_label"))
}

// === V39 — HubSync gating 3 phases ===

// newPaywallReqWithPath crée une requête POST sur un path donné.
func newPaywallReqWithPath(t *testing.T, path, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// freshPlan retourne un plan actif avec last_hub_sync_at frais (< 24h).
func freshPlan(workspaceID string) *domain.VeridianPlan {
	syncAt := time.Now().Add(-1 * time.Hour)
	return &domain.VeridianPlan{
		WorkspaceID:   workspaceID,
		Plan:          "pro",
		Status:        domain.PlanStatusActive,
		LastHubSyncAt: &syncAt,
	}
}

// stalePlan retourne un plan actif avec last_hub_sync_at stale (25h).
func stalePlan(workspaceID string) *domain.VeridianPlan {
	syncAt := time.Now().Add(-25 * time.Hour)
	return &domain.VeridianPlan{
		WorkspaceID:   workspaceID,
		Plan:          "pro",
		Status:        domain.PlanStatusActive,
		LastHubSyncAt: &syncAt,
	}
}

// deadPlan retourne un plan actif avec last_hub_sync_at dead (73h).
func deadPlan(workspaceID string) *domain.VeridianPlan {
	syncAt := time.Now().Add(-73 * time.Hour)
	return &domain.VeridianPlan{
		WorkspaceID:   workspaceID,
		Plan:          "pro",
		Status:        domain.PlanStatusActive,
		LastHubSyncAt: &syncAt,
	}
}

func TestVeridianPaywall_HubSyncFresh_WritePasses(t *testing.T) {
	// Tenant fresh (< 24h) + write → passe normalement.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-fresh").Return(freshPlan("ws-fresh"), nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-fresh"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called.Load(), "write frais doit passer")
}

func TestVeridianPaywall_HubSyncStale_WritePasses(t *testing.T) {
	// Tenant stale (25h) + write → passe (grace optimistic), no 503.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-stale").Return(stalePlan("ws-stale"), nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-stale"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "stale doit passer (grace optimistic)")
	assert.True(t, called.Load())
}

func TestVeridianPaywall_HubSyncDead_WritePasses(t *testing.T) {
	// Fix 2026-05-30 : un tenant "dead" (73h sans op Hub) ne bloque PLUS les
	// writes. Notifuse est stand-alone — aucun write ne dépend du Hub.
	// (Avant : 503 hub_sync_dead. Le blocage était un faux positif structurel
	// + une violation de la règle d'or "chaque app marche seule".)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-dead").Return(deadPlan("ws-dead"), nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-dead"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "dead write doit PASSER (stand-alone, pas de blocage)")
	assert.True(t, called.Load(), "next handler DOIT être appelé")
	body := rec.Body.String()
	assert.NotContains(t, body, "hub_sync_dead", "plus de blocage hub_sync_dead")
}

func TestVeridianPaywall_HubSyncDead_ReadPasses(t *testing.T) {
	// Tenant dead (73h) + GET → passe (reads best-effort).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Le middleware laisse passer les GET sans lire le body.
	var called atomic.Bool
	// planRepo non appelé car GET → early return avant lookup
	mw := NewVeridianPaywallMiddleware(nil, logger.NewLogger())(nextHandler(&called))

	r := httptest.NewRequest(http.MethodGet, "/api/transactional.send", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, r)

	assert.Equal(t, http.StatusOK, rec.Code, "GET doit passer (pas de body à inspecter)")
	assert.True(t, called.Load())
}

func TestVeridianPaywall_HubSyncDead_AdminRouteExempted(t *testing.T) {
	// Tenant dead (73h) + route admin /api/veridian/* → passe (Hub doit pouvoir réveiller).
	// Note : le middleware du path filter routera les routes admin directement vers next,
	// mais on teste ici isHubSyncWriteBlock directement.
	r := httptest.NewRequest(http.MethodPost, "/api/veridian/admin/touch", nil)
	assert.False(t, isHubSyncWriteBlock(r),
		"/api/veridian/* doit être exemptée du blocage dead")

	r2 := httptest.NewRequest(http.MethodPost, "/api/transactional.send", nil)
	assert.True(t, isHubSyncWriteBlock(r2),
		"/api/transactional.send doit être bloquée en mode dead")
}

func TestVeridianPaywall_HubSyncDead_SoftDeletedPrimes(t *testing.T) {
	// Tenant soft-deleted ET dead → soft-deleted prime (UX cohérent).
	// IsBlocked retourne true pour deleted → réponse 402 (pas 503).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	deletedAt := time.Now().Add(-73 * time.Hour)
	syncAt := time.Now().Add(-73 * time.Hour)
	plan := &domain.VeridianPlan{
		WorkspaceID:   "ws-deleted-dead",
		Plan:          "pro",
		Status:        domain.PlanStatusDeleted,
		DeletedAt:     &deletedAt,
		LastHubSyncAt: &syncAt,
	}
	repo := mocks.NewMockVeridianPlanRepository(ctrl)
	repo.EXPECT().Get(gomock.Any(), "ws-deleted-dead").Return(plan, nil).Times(1)

	var called atomic.Bool
	mw := NewVeridianPaywallMiddleware(repo, logger.NewLogger())(nextHandler(&called))

	req := newPaywallReq(t, `{"workspace_id":"ws-deleted-dead"}`)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// Soft-deleted prime : 402, pas 503.
	assert.Equal(t, http.StatusPaymentRequired, rec.Code,
		"soft-deleted prime sur HubSyncDead → 402 pas 503")
	assert.False(t, called.Load())
}

func TestShouldLogStale_RateLimit(t *testing.T) {
	// Nettoyer l'état global avant le test.
	staleLogState.Delete("ws-rate-limit-test")
	defer staleLogState.Delete("ws-rate-limit-test")

	// Premier appel → doit logger.
	assert.True(t, shouldLogStale("ws-rate-limit-test"), "premier appel = doit logger")
	// Deuxième appel immédiat → ne doit PAS logger (< 1 min).
	assert.False(t, shouldLogStale("ws-rate-limit-test"), "appel immédiat = rate-limited")
}

// === Has() — observation cache state (AUDIT-TRIAL-RESIDUS-2026-05-24) ===
//
// Has() expose un read non-destructif sur l'état du cache, sans changer
// son contenu (sauf nettoyage TTL expiré, cohérent avec get()). Utilisé
// par les tests anti-régression du pattern d'invalidation post-mutation
// (cf. veridian_handler_test.go §AUDIT-TRIAL-RESIDUS-2026-05-24).

func TestPaywallCache_Has_EmptyCache(t *testing.T) {
	cache := NewPaywallCache()
	assert.False(t, cache.Has("ws-1"), "cache vide → Has retourne false")
}

func TestPaywallCache_Has_AfterSeed(t *testing.T) {
	cache := NewPaywallCache()
	cache.SeedForTest("ws-seeded")
	assert.True(t, cache.Has("ws-seeded"), "post-seed → Has retourne true")
	// L'appel précédent ne doit pas consommer l'entrée (Has est idempotent).
	assert.True(t, cache.Has("ws-seeded"), "Has est idempotent — entrée non consommée")
}

func TestPaywallCache_Has_AfterInvalidate(t *testing.T) {
	cache := NewPaywallCache()
	cache.SeedForTest("ws-tobeinvalidated")
	require.True(t, cache.Has("ws-tobeinvalidated"))
	cache.Invalidate("ws-tobeinvalidated")
	assert.False(t, cache.Has("ws-tobeinvalidated"), "post-Invalidate → Has retourne false")
}

func TestPaywallCache_Has_AfterClear(t *testing.T) {
	cache := NewPaywallCache()
	cache.SeedForTest("ws-A")
	cache.SeedForTest("ws-B")
	require.True(t, cache.Has("ws-A"))
	require.True(t, cache.Has("ws-B"))
	cache.Clear()
	assert.False(t, cache.Has("ws-A"), "post-Clear → tous absents")
	assert.False(t, cache.Has("ws-B"), "post-Clear → tous absents")
}

func TestPaywallCache_Has_IndependentEntries(t *testing.T) {
	// Invalidate(A) ne doit pas toucher B.
	cache := NewPaywallCache()
	cache.SeedForTest("ws-A")
	cache.SeedForTest("ws-B")
	cache.Invalidate("ws-A")
	assert.False(t, cache.Has("ws-A"), "A invalidé")
	assert.True(t, cache.Has("ws-B"), "B intact — Invalidate ne doit toucher que l'entrée ciblée")
}

// === SeedForTest() — sentinelle test-only ===
//
// SeedForTest crée une entrée notFound:true avec TTL standard 60s. Utilisé
// par les tests anti-régression qui veulent observer une invalidation
// sans monter le middleware complet + planRepo mocké.

func TestPaywallCache_SeedForTest_CreatesEntry(t *testing.T) {
	cache := NewPaywallCache()
	assert.False(t, cache.Has("ws-x"), "avant seed")
	cache.SeedForTest("ws-x")
	assert.True(t, cache.Has("ws-x"), "après seed")
}

func TestPaywallCache_SeedForTest_Overwrite(t *testing.T) {
	// Re-seed sur la même clé doit rester idempotent (overwrite OK).
	cache := NewPaywallCache()
	cache.SeedForTest("ws-double")
	cache.SeedForTest("ws-double") // ne doit pas paniquer
	assert.True(t, cache.Has("ws-double"))
}

func TestIsHubSyncWriteBlock(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/transactional.send", true},
		{http.MethodPut, "/api/broadcasts.create", true},
		{http.MethodDelete, "/api/broadcasts.schedule", true},
		{http.MethodGet, "/api/transactional.send", false},
		{http.MethodHead, "/api/transactional.send", false},
		{http.MethodPost, "/api/veridian/admin/touch", false},   // admin exempt
		{http.MethodPost, "/api/veridian/admin/resume", false},  // admin exempt
		{http.MethodPost, "/api/veridian/", false},              // admin prefix exempt
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, tc.want, isHubSyncWriteBlock(r))
		})
	}
}
