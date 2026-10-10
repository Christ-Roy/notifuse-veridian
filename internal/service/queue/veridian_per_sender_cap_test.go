package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Réutilise les helpers du package (newVeridianThrottleTestEnv, veridianTestEntry)
// définis dans veridian_provider_throttle_test.go, et veridianTestWorkspaceWithCaps
// défini dans veridian_daily_cap_test.go.

func veridianTestWorkspaceWithSenderCap(perSenderCap int) *domain.Workspace {
	ws := veridianTestWorkspaceWithCaps(nil, 0)
	ws.Settings.VeridianPerSenderDailyCap = perSenderCap
	return ws
}

func veridianTestEntryWithSender(id, recipient, sender string, payload domain.EmailQueuePayload) *domain.EmailQueueEntry {
	payload.FromAddress = sender
	return veridianTestEntry(id, recipient, payload)
}

func TestVeridianResolvePerSenderCap(t *testing.T) {
	t.Run("payload overrides infra and workspace", func(t *testing.T) {
		ws := veridianTestWorkspaceWithSenderCap(50)
		provider := &domain.EmailProvider{VeridianPerSenderDailyCap: 10}
		entry := veridianTestEntryWithSender("e", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{
			VeridianPerSenderDailyCap: 1,
		})
		assert.Equal(t, 1, veridianResolvePerSenderCap(ws, provider, entry))
	})

	t.Run("infra overrides workspace when payload empty", func(t *testing.T) {
		ws := veridianTestWorkspaceWithSenderCap(50)
		provider := &domain.EmailProvider{VeridianPerSenderDailyCap: 5}
		entry := veridianTestEntryWithSender("e", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})
		assert.Equal(t, 5, veridianResolvePerSenderCap(ws, provider, entry))
	})

	t.Run("falls back to workspace when payload and infra empty", func(t *testing.T) {
		ws := veridianTestWorkspaceWithSenderCap(7)
		entry := veridianTestEntryWithSender("e", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})
		assert.Equal(t, 7, veridianResolvePerSenderCap(ws, nil, entry))
	})

	t.Run("nil everywhere = 0 (no cap)", func(t *testing.T) {
		entry := veridianTestEntryWithSender("e", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})
		assert.Equal(t, 0, veridianResolvePerSenderCap(nil, nil, entry))
	})
}

func TestVeridianPerSenderCapGate_NoConfigIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(0)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})

	// Aucun cap émetteur : aucun COUNT, jamais cappé.
	delay, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianPerSenderCapGate_NoFromAddressIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(1)
	// Cap configuré mais pas d'adresse FROM : on ne peut pas attribuer l'envoi à
	// une boîte → best-effort, on n'enforce pas (aucun COUNT attendu).
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "", domain.EmailQueuePayload{})

	delay, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianPerSenderCapGate_CapReached(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(1) // 1 mail/jour/boîte (warmup J1)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{})

	// Déjà 1 envoi aujourd'hui depuis cette boîte → cap atteint → skip.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).
		Return(1, nil)

	delay, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianPerSenderCapGate_UnderCapPasses(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(5)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{})

	// 4 < 5 → passe.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).
		Return(4, nil)

	delay, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.False(t, capped)
	assert.Zero(t, delay)
}

func TestVeridianPerSenderCapGate_InfraCapApplied(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(0) // pas de cap workspace
	provider := &domain.EmailProvider{VeridianPerSenderDailyCap: 2}
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).
		Return(2, nil)

	delay, capped := env.worker.veridianPerSenderCapGate(ws, provider, entry)
	assert.True(t, capped)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianPerSenderCapGate_CountErrorDegradesToAllow(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(1)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).
		Return(0, errors.New("db down"))

	delay, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.False(t, capped, "une erreur de COUNT ne doit jamais bloquer l'envoi")
	assert.Zero(t, delay)
}

func TestVeridianPerSenderCapGate_PayloadCapApplied(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// Cap posé au niveau broadcast (payload), prime sur workspace/infra.
	ws := veridianTestWorkspaceWithSenderCap(100)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{
		VeridianPerSenderDailyCap: 1,
	})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).
		Return(1, nil)

	_, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.True(t, capped)
}

// Lot 4 (08/10/2026) : le plafond par adresse se compte depuis minuit heure de Paris.
func TestVeridianPerSenderCapGate_CountsSinceParisMidnight(t *testing.T) {
	withFixedClock(t, time.Date(2026, 10, 8, 22, 30, 0, 0, time.UTC)) // 00h30 le 9 octobre a Paris
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(3)
	provider := parisWindowProvider()
	entry := veridianTestEntryWithSender("e", "lead@gmail.com", "bot@envoi.example", domain.EmailQueuePayload{})

	var since time.Time
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "bot@envoi.example", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, s time.Time) (int, error) { since = s; return 3, nil })

	_, capped := env.worker.veridianPerSenderCapGate(ws, provider, entry)
	assert.True(t, capped)
	assert.Equal(t, time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), since)
}

// --- Fiche 62 : variante structurée veridianPerSenderCapVerdict ---

func TestVeridianPerSenderCapVerdict_BlockedCarriesCountAndCap(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(5)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{})
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).Return(7, nil)

	v := env.worker.veridianPerSenderCapVerdict(ws, nil, entry)
	require.True(t, v.Blocked())
	assert.Equal(t, domain.VeridianGateSenderCap, v.Gate, "la porte est sender_cap, pas daily_cap")
	assert.Equal(t, domain.VeridianVerdictBlock, v.Verdict)
	assert.Equal(t, 7, v.Value)
	assert.Equal(t, 5, v.Limit)
	assert.Equal(t, "per_sender", v.Name)
	assert.Equal(t, domain.VeridianReasonCapacity, v.Reason)
	assert.Equal(t, veridianDailyCapRecheckInterval, v.Delay)
}

func TestVeridianPerSenderCapVerdict_BoundaryAndPass(t *testing.T) {
	for _, tc := range []struct {
		count   int
		blocked bool
	}{{4, false}, {5, true}, {6, true}} {
		env := newVeridianThrottleTestEnv(t)
		ws := veridianTestWorkspaceWithSenderCap(5)
		entry := veridianTestEntryWithSender("e1", "a@gmail.com", "warmup@send.fr", domain.EmailQueuePayload{})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForSender(gomock.Any(), "ws-1", "warmup@send.fr", gomock.Any()).Return(tc.count, nil)

		v := env.worker.veridianPerSenderCapVerdict(ws, nil, entry)
		assert.Equal(t, tc.blocked, v.Blocked(), "count=%d cap=5", tc.count)
		assert.Equal(t, tc.count, v.Value)
		assert.Equal(t, 5, v.Limit)
		assert.Equal(t, "per_sender", v.Name)
		if !tc.blocked {
			assert.Equal(t, domain.VeridianVerdictPass, v.Verdict)
			assert.Zero(t, v.Delay)
		}
	}
}

func TestVeridianPerSenderCapVerdict_NoConfigNoSenderAndCountErrorPassWithTheirOwnExplanation(t *testing.T) {
	t.Run("no cap", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		v := env.worker.veridianPerSenderCapVerdict(veridianTestWorkspaceWithSenderCap(0), nil,
			veridianTestEntryWithSender("e1", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{}))
		assert.False(t, v.Blocked())
		assert.Nil(t, v.Value)
		assert.Nil(t, v.Limit)
		assert.Contains(t, v.Detail, "no per-sender cap")
	})
	t.Run("no sender address: cap known, not enforced", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		v := env.worker.veridianPerSenderCapVerdict(veridianTestWorkspaceWithSenderCap(3), nil,
			veridianTestEntryWithSender("e1", "a@gmail.com", "", domain.EmailQueuePayload{}))
		assert.False(t, v.Blocked())
		assert.Nil(t, v.Value)
		assert.Equal(t, 3, v.Limit)
		assert.Contains(t, v.Detail, "not enforced")
	})
	t.Run("count error: allowed but flagged degraded", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestEntryWithSender("e1", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})
		env.mockMessageHistoryRepo.EXPECT().
			CountSentSinceForSender(gomock.Any(), "ws-1", "bot@send.fr", gomock.Any()).Return(0, errors.New("db down"))
		v := env.worker.veridianPerSenderCapVerdict(veridianTestWorkspaceWithSenderCap(3), nil, entry)
		assert.False(t, v.Blocked(), "une erreur de COUNT ne bloque jamais (différent du plafond journalier)")
		assert.Nil(t, v.Value)
		assert.Equal(t, 3, v.Limit)
		assert.Contains(t, v.Detail, "degraded")
	})
}

func TestVeridianPerSenderCapGate_WrapperMatchesVerdict(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspaceWithSenderCap(1)
	entry := veridianTestEntryWithSender("e1", "a@gmail.com", "bot@send.fr", domain.EmailQueuePayload{})
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "bot@send.fr", gomock.Any()).Return(1, nil).Times(2)

	v := env.worker.veridianPerSenderCapVerdict(ws, nil, entry)
	delay, capped := env.worker.veridianPerSenderCapGate(ws, nil, entry)
	assert.True(t, capped)
	assert.Equal(t, v.Blocked(), capped)
	assert.Equal(t, v.Delay, delay)
}
