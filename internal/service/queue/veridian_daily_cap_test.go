package queue

import (
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// Réutilise les helpers du package (newVeridianThrottleTestEnv, veridianTestEntry)
// définis dans veridian_provider_throttle_test.go.

func veridianTestWorkspaceWithCaps(classCaps map[string]int, perRecipientCap int) *domain.Workspace {
	return &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			VeridianProviderClassDailyCap: classCaps,
			VeridianPerRecipientDailyCap:  perRecipientCap,
		},
		Integrations: []domain.Integration{
			{
				ID: "int-1",
				EmailProvider: domain.EmailProvider{
					Kind:               domain.EmailProviderKindSMTP,
					RateLimitPerMinute: 6000,
				},
			},
		},
	}
}

func TestVeridianStartOfDayUTC(t *testing.T) {
	in := time.Date(2026, 6, 14, 15, 30, 45, 999, time.FixedZone("CEST", 2*3600))
	got := veridianStartOfDayUTC(in)
	// 15:30 CEST = 13:30 UTC → minuit UTC du 14.
	assert.Equal(t, time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), got)
	assert.Equal(t, time.UTC, got.Location())
}

func TestVeridianResolveDailyCaps(t *testing.T) {
	t.Run("payload overrides workspace", func(t *testing.T) {
		ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 100}, 9)
		entry := veridianTestEntry("e", "a@gmail.com", domain.EmailQueuePayload{
			VeridianProviderClassDailyCap: map[string]int{"google": 1},
			VeridianPerRecipientDailyCap:  1,
		})
		classCaps, perRecipient := veridianResolveDailyCaps(ws, nil, entry)
		assert.Equal(t, map[string]int{"google": 1}, classCaps)
		assert.Equal(t, 1, perRecipient)
	})

	t.Run("falls back to workspace when payload empty", func(t *testing.T) {
		ws := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 5}, 3)
		entry := veridianTestEntry("e", "a@outlook.com", domain.EmailQueuePayload{})
		classCaps, perRecipient := veridianResolveDailyCaps(ws, nil, entry)
		assert.Equal(t, map[string]int{"microsoft": 5}, classCaps)
		assert.Equal(t, 3, perRecipient)
	})

	t.Run("nil workspace safe", func(t *testing.T) {
		entry := veridianTestEntry("e", "a@gmail.com", domain.EmailQueuePayload{VeridianPerRecipientDailyCap: 2})
		classCaps, perRecipient := veridianResolveDailyCaps(nil, nil, entry)
		assert.Nil(t, classCaps)
		assert.Equal(t, 2, perRecipient)
	})
}

func TestVeridianDailyCapGate_NoConfigIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	// Aucun cap : aucun COUNT ne doit être appelé, jamais cappé.
	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_PerRecipientReached(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 1) // 1 mail/jour/destinataire
	entry := veridianTestEntry("e1", "victim@gmail.com", domain.EmailQueuePayload{})

	// Déjà 1 envoi aujourd'hui → cap atteint → skip.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "victim@gmail.com", gomock.Any()).
		Return(1, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_PerRecipientUnderCapPasses(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(nil, 2)
	entry := veridianTestEntry("e1", "ok@gmail.com", domain.EmailQueuePayload{})

	// 1 envoi < cap 2 → passe.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "ok@gmail.com", gomock.Any()).
		Return(1, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_ClassReached(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 50}, 0)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	// gmail.com → classe google → domaines non exclus, COUNT = 50 ≥ cap → skip.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
		Return(50, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_ClassUnderCapPasses(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 50}, 0)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
		Return(49, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_CorporateUsesExclusion(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"corporate": 10}, 0)
	// Classe `corporate` portée explicitement (tag amont) : depuis le Lot 4, un
	// domaine custom inconnu est classé par MX (→ corporate_selfhost) ; le chemin
	// d'EXCLUSION par domaines connus reste spécifique à la classe HISTORIQUE
	// `corporate`. On la pose donc via le tag pour couvrir exactement ce chemin.
	entry := veridianTestEntry("e1", "ceo@acme-corp.com", domain.EmailQueuePayload{
		VeridianProviderClass: domain.ProviderClassCorporate,
	})

	// classe corporate → exclude=true sur la liste des domaines connus.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), true, gomock.Any()).
		Return(10, nil)

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_PerRecipientWinsOverClass(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Le destinataire est testé EN PREMIER. S'il est atteint, on skip sans
	// même interroger le COUNT classe (le plus restrictif gagne, court-circuit).
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1000}, 1)
	entry := veridianTestEntry("e1", "victim@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "victim@gmail.com", gomock.Any()).
		Return(1, nil)
	// CountSentSinceForDomains NE doit PAS être appelé (court-circuit) — pas d'EXPECT.

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_ClassCheckedWhenRecipientUnderCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 5}, 10)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	// destinataire sous le cap (0 < 10) → on continue vers le cap classe.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "lead@gmail.com", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
		Return(5, nil) // classe atteinte → skip

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_CountErrorDegradesToAllow(t *testing.T) {
	t.Run("per-recipient count error → allow (best-effort)", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		ws := veridianTestWorkspaceWithCaps(nil, 1)
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForContact(gomock.Any(), "ws-1", "a@gmail.com", gomock.Any()).
			Return(0, errors.New("db down"))

		delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.False(t, capped, "une erreur de COUNT ne doit jamais bloquer l'envoi")
		assert.Zero(t, delay)
	})

	t.Run("class count error → allow (best-effort)", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1}, 0)
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
			Return(0, errors.New("db down"))

		delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
		assert.False(t, capped)
		assert.Zero(t, delay)
	})
}

func TestVeridianDailyCapGate_ClassWithoutCapForResolvedClassSkipsCount(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// cap posé seulement sur microsoft, le destinataire est gmail (google) →
	// la classe google n'a pas de cap → aucun COUNT classe, passe.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 5}, 0)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func veridianWarmupTestProvider(startedAt time.Time, schedule []int, stepDays int) *domain.EmailProvider {
	return &domain.EmailProvider{
		Kind:                    domain.EmailProviderKindSMTP,
		RateLimitPerMinute:      6000,
		VeridianWarmupStartedAt: &startedAt,
		VeridianWarmupSchedule:  schedule,
		VeridianWarmupStepDays:  stepDays,
	}
}

func TestVeridianWarmupClassCap(t *testing.T) {
	base := time.Now().UTC()
	assert.Equal(t, 0, veridianWarmupClassCap(nil, base), "nil provider = no warmup")
	assert.Equal(t, 0, veridianWarmupClassCap(&domain.EmailProvider{}, base), "no warmup config = 0")

	// Démarré aujourd'hui, courbe [1,2,5], palier 1 jour → jour 0 = 1.
	p := veridianWarmupTestProvider(base, []int{1, 2, 5}, 1)
	assert.Equal(t, 1, veridianWarmupClassCap(p, base))
}

func TestVeridianDailyCapGate_WarmupOverridesStaticClassCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Workspace pose un cap statique généreux (google 1000), mais l'infra est en
	// warmup jour 0 (cap 1) → le warmup PRIME → 1 envoi déjà fait aujourd'hui = skip.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"google": 1000}, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{1, 2, 5}, 1)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
		Return(1, nil) // 1 >= warmupCap(1) → skip

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.True(t, capped, "le cap warmup (1) prime sur le cap statique (1000)")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianDailyCapGate_WarmupAppliesEvenWithoutStaticClassCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Aucun cap statique configuré nulle part, mais l'infra est en warmup → le cap
	// warmup s'enforce quand même (sur la classe google adossée à un suffixe).
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{2, 5}, 1)
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
		Return(1, nil) // 1 < warmupCap(2) → passe

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.False(t, capped, "jour 0 cap=2, déjà 1 envoyé → passe")
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_NoWarmupNoConfigStillNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Provider sans warmup + pas de cap → toujours no-op (non-régression).
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	provider := &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 6000}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianDailyCapGate_PayloadTagDrivesClass(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Le tag payload force la classe microsoft même si l'email est corporate.
	ws := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	entry := veridianTestEntry("e1", "ceo@acme-corp.com", domain.EmailQueuePayload{
		VeridianProviderClass: domain.ProviderClassMicrosoft,
	})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomains(gomock.Any(), "ws-1", gomock.Any(), false, gomock.Any()).
		Return(1, nil)

	_, capped := env.worker.veridianDailyCapGate(ws, nil, entry)
	assert.True(t, capped)
}
