package queue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type veridianThrottleTestEnv struct {
	worker                 *EmailQueueWorker
	mockQueueRepo          *mocks.MockEmailQueueRepository
	mockEmailService       *mocks.MockEmailServiceInterface
	mockMessageHistoryRepo *mocks.MockMessageHistoryRepository
}

func newVeridianThrottleTestEnv(t *testing.T) *veridianThrottleTestEnv {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	worker := NewEmailQueueWorker(
		mockQueueRepo,
		mockWorkspaceRepo,
		mockEmailService,
		mockMessageHistoryRepo,
		DefaultWorkerConfig(),
		mockLogger,
	)
	worker.ctx = context.Background()

	return &veridianThrottleTestEnv{
		worker:                 worker,
		mockQueueRepo:          mockQueueRepo,
		mockEmailService:       mockEmailService,
		mockMessageHistoryRepo: mockMessageHistoryRepo,
	}
}

func veridianTestWorkspace(classRates map[string]float64, emitterRatePerMinute int) *domain.Workspace {
	return &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			VeridianProviderClassRates: classRates,
		},
		Integrations: []domain.Integration{
			{
				ID: "int-1",
				EmailProvider: domain.EmailProvider{
					Kind:               domain.EmailProviderKindSMTP,
					RateLimitPerMinute: emitterRatePerMinute,
				},
			},
		},
	}
}

func veridianTestEntry(id, email string, payload domain.EmailQueuePayload) *domain.EmailQueueEntry {
	if payload.RateLimitPerMinute == 0 {
		payload.RateLimitPerMinute = 6000
	}
	return &domain.EmailQueueEntry{
		ID:            id,
		Status:        domain.EmailQueueStatusPending,
		SourceType:    domain.EmailQueueSourceBroadcast,
		SourceID:      "broadcast-1",
		IntegrationID: "int-1",
		ContactEmail:  email,
		MessageID:     "msg-" + id,
		Payload:       payload,
		MaxAttempts:   3,
	}
}

func TestVeridianProviderClassGate_NoConfigIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	// Aucune config (ni payload ni workspace) : jamais throttlé, même appelé
	// en rafale — non-régression stricte du comportement upstream.
	for i := 0; i < 10; i++ {
		delay, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
		assert.False(t, throttled)
		assert.Zero(t, delay)
	}
}

func TestVeridianProviderClassGate_WorkspaceDefaultsApply(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	// 1er passage : token consommé, pas de throttle
	delay, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.False(t, throttled)
	assert.Zero(t, delay)

	// 2e passage immédiat : throttlé, délai ≈ 60s (1/min), borné [1s, 5min]
	delay, throttled = env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.True(t, throttled)
	assert.GreaterOrEqual(t, delay, time.Second)
	assert.LessOrEqual(t, delay, veridianMaxProviderClassRetryDelay)
}

func TestVeridianProviderClassGate_PayloadRatesTakePrecedence(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Workspace ne throttle PAS google ; le payload (broadcast) si.
	workspace := veridianTestWorkspace(map[string]float64{"microsoft": 1}, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{
		VeridianProviderClassRates: map[string]float64{"google": 1},
	})

	_, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	require.False(t, throttled)
	_, throttled = env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.True(t, throttled, "les débits du broadcast (payload) doivent primer sur le workspace")
}

func TestVeridianProviderClassGate_ContactTagOverridesClassification(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)

	// Email corporate mais taggué google en amont (ex. MX Google Workspace
	// résolu par Prospection) : c'est le bucket google qui s'applique.
	entry := veridianTestEntry("e1", "contact@boitepro.fr", domain.EmailQueuePayload{
		VeridianProviderClass: "google",
	})

	_, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	require.False(t, throttled)
	_, throttled = env.worker.veridianProviderClassGate(workspace, nil, entry)
	assert.True(t, throttled, "le tag contact doit primer sur la classification par suffixe")
}

func TestVeridianProviderClassGate_UnconfiguredClassNotThrottled(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Seul google est contraint : le corporate file sans limite de classe.
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	entry := veridianTestEntry("e1", "contact@boitepro.fr", domain.EmailQueuePayload{})

	for i := 0; i < 10; i++ {
		_, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
		assert.False(t, throttled)
	}
}

func TestVeridianProviderClassGate_DelayCappedForSlowRates(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// 0.005/min ≈ 7/jour (warm-up) : délai théorique 200min, borné à 5min
	// pour re-checker la config régulièrement.
	workspace := veridianTestWorkspace(map[string]float64{"google": 0.005}, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	_, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	require.False(t, throttled)
	delay, throttled := env.worker.veridianProviderClassGate(workspace, nil, entry)
	require.True(t, throttled)
	assert.Equal(t, veridianMaxProviderClassRetryDelay, delay)
}

func TestProcessEntry_ProviderClassThrottleSkipsWithoutAttempt(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)

	entry1 := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
	entry2 := veridianTestEntry("e2", "b@gmail.com", domain.EmailQueuePayload{})

	// entry1 consomme le token google : envoi complet
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)

	// entry2 : bucket google vide → re-planifiée SANS MarkAsProcessing
	// (donc sans incrément d'attempts) et SANS envoi
	env.mockQueueRepo.EXPECT().
		SetNextRetry(gomock.Any(), "ws-1", "e2", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, nextRetry time.Time) error {
			assert.WithinDuration(t, time.Now().Add(60*time.Second), nextRetry, 5*time.Second)
			return nil
		})

	env.worker.processEntry(workspace, entry1)
	env.worker.processEntry(workspace, entry2)

	// gomock (ctrl.Finish) garantit qu'aucun appel non déclaré n'a eu lieu :
	// pas de MarkAsProcessing ni SendEmail pour e2.
}

func TestProcessEntry_MixedBatchClassesDoNotBlockEachOther(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// google bridé à 1/min ; émetteur à 600/min (~100ms entre envois) pour
	// prouver que les DEUX étages composent.
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 600)

	gmail1 := veridianTestEntry("g1", "a@gmail.com", domain.EmailQueuePayload{})
	gmail2 := veridianTestEntry("g2", "b@gmail.com", domain.EmailQueuePayload{})
	corp1 := veridianTestEntry("c1", "x@boitepro.fr", domain.EmailQueuePayload{})
	corp2 := veridianTestEntry("c2", "y@acme-corp.com", domain.EmailQueuePayload{})

	var sent []string
	for _, id := range []string{"g1", "c1", "c2"} {
		id := id
		env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", id).Return(nil)
		env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", id).
			DoAndReturn(func(_ context.Context, _ string, entryID string) error {
				sent = append(sent, entryID)
				return nil
			})
	}
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil).Times(3)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil).Times(3)

	// gmail2 est le seul re-planifié : sa classe est saturée, pas les autres
	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "g2", gomock.Any()).Return(nil)

	start := time.Now()
	env.worker.processEntry(workspace, gmail1)
	env.worker.processEntry(workspace, gmail2) // skip immédiat, ne bloque pas
	env.worker.processEntry(workspace, corp1)
	env.worker.processEntry(workspace, corp2)
	elapsed := time.Since(start)

	assert.ElementsMatch(t, []string{"g1", "c1", "c2"}, sent)

	// Composition : le limiter émetteur (600/min, burst 1) impose ~100ms
	// entre chacun des 3 envois réels → ≥ ~180ms au total. Si le gate classe
	// court-circuitait l'étage émetteur, tout partirait en < 10ms.
	assert.GreaterOrEqual(t, elapsed, 180*time.Millisecond,
		"le throttle émetteur doit rester appliqué par-dessus le throttle classe")
}

// Anti busy-loop (vigilance lead, CONTRATS-TUNNEL.md §1) : une queue saturée
// d'une SEULE classe throttlée (50 gmail à 1/min) ne doit ni bloquer le
// worker (pas de Wait de 49 minutes), ni le faire tourner à vide : chaque
// entrée sans token est re-planifiée dans le FUTUR (≥ ~1s) via SetNextRetry,
// donc exclue des FetchPending suivants (WHERE next_retry_at <= NOW())
// jusqu'à l'arrivée du prochain token. Un seul envoi réel consomme le token.
// Le worker traite jusqu'à WorkerCount WORKSPACES en parallèle
// (processAllWorkspaces), tous partageant le MÊME providerClassLimiter. Le gate
// veridianProviderClassGate est donc appelé concurremment. On prouve l'absence
// de data race sur ce chemin (gate → limiter.Allow → SetLimit), sur des
// workspaces et classes variés. À lancer avec -race.
func TestVeridianProviderClassGate_ConcurrentWorkspacesNoRace(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)

	const goroutines = 16
	const iterations = 300
	emails := []string{"a@gmail.com", "b@outlook.fr", "c@yahoo.fr", "d@orange.fr", "e@acme.fr"}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			// Workspaces distincts mais throttle config sur les mêmes classes :
			// le providerClassLimiter partagé voit des clés integrationID|classe
			// qui se chevauchent quand g pointe la même intégration.
			ws := &domain.Workspace{
				ID: fmt.Sprintf("ws-%d", g%4),
				Settings: domain.WorkspaceSettings{
					VeridianProviderClassRates: map[string]float64{
						"google": 1, "microsoft": 2, "yahoo_aol": 5,
					},
				},
				Integrations: []domain.Integration{{
					ID:            fmt.Sprintf("int-%d", g%4),
					EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 6000},
				}},
			}
			for i := 0; i < iterations; i++ {
				entry := veridianTestEntry(
					fmt.Sprintf("e-%d-%d", g, i), emails[i%len(emails)], domain.EmailQueuePayload{})
				entry.IntegrationID = ws.Integrations[0].ID
				// On ne fait que solliciter le gate (lecture + Allow) : pas de
				// repo touché, c'est le chemin concurrent partagé qu'on stresse.
				env.worker.veridianProviderClassGate(ws, nil, entry)
			}
		}(g)
	}
	wg.Wait()

	// Critère : pas de race/panic. Les stats restent lisibles après la tempête.
	_ = env.worker.GetProviderClassStats()
}

func TestProcessEntry_SaturatedClassNoBusyLoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)

	const total = 50
	entries := make([]*domain.EmailQueueEntry, 0, total)
	for i := 0; i < total; i++ {
		entries = append(entries, veridianTestEntry(
			fmt.Sprintf("g%02d", i), "user@gmail.com", domain.EmailQueuePayload{}))
	}

	// Exactement 1 envoi complet (le token du bucket google)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", entries[0].ID).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil).Times(1)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", entries[0].ID).Return(nil)

	// Les 49 autres : re-planifiées strictement dans le futur (≥ ~1s), jamais
	// envoyées, jamais MarkAsProcessing (pas d'attempt brûlé)
	start := time.Now()
	env.mockQueueRepo.EXPECT().
		SetNextRetry(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, nextRetry time.Time) error {
			assert.True(t, nextRetry.After(start.Add(900*time.Millisecond)),
				"re-schedule trop proche = risque de busy-loop au prochain poll")
			return nil
		}).Times(total - 1)

	for _, e := range entries {
		env.worker.processEntry(workspace, e)
	}
	elapsed := time.Since(start)

	// Tout le batch saturé est traité quasi instantanément (skip non bloquant) :
	// un design à Wait bloquant aurait pris ~49 minutes ici.
	assert.Less(t, elapsed, 5*time.Second,
		"une classe saturée ne doit pas bloquer le worker pool")
}
