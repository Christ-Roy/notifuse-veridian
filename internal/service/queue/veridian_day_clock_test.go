package queue

import (
	"context"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lot 4 (08/10/2026) : le jour de compte des plafonds suit le fuseau de la fenetre
// d'envoi du profil (Europe/Paris), plus minuit UTC.

func withFixedClock(t *testing.T, at time.Time) {
	t.Helper()
	old := veridianNow
	veridianNow = func() time.Time { return at }
	t.Cleanup(func() { veridianNow = old })
}

func parisWindowProvider() *domain.EmailProvider {
	return &domain.EmailProvider{
		Kind:                    domain.EmailProviderKindSMTP,
		VeridianProfileDailyCap: 30,
		VeridianSendingWindow:   &domain.VeridianSendingWindow{StartHour: 8, EndHour: 19, Timezone: "Europe/Paris"},
	}
}

func reserveAt(t *testing.T, at time.Time) domain.VeridianDailyQuotaReservation {
	t.Helper()
	withFixedClock(t, at)
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{outcomes: map[string]quotaTestOutcome{
		domain.VeridianDailyQuotaKindProfile: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
	}}
	env.worker.messageHistoryRepo = repo
	entry := veridianTestEntryFrom("q", "lead@gmail.com", "hello@envoi.example", domain.EmailQueuePayload{})
	entry.IntegrationID = "p1"
	_, _, blocked := env.worker.veridianReserveDailyQuota(veridianTestWorkspace(nil, 60), parisWindowProvider(), entry)
	require.False(t, blocked)
	require.Len(t, repo.specs, 1)
	return repo.specs[0]
}

func TestReserveDailyQuota_DayFollowsParisWindowNotUTC(t *testing.T) {
	// 8 octobre 2026, 21h30 UTC = 23h30 a Paris : encore le 8.
	late := reserveAt(t, time.Date(2026, 10, 8, 21, 30, 0, 0, time.UTC))
	assert.Equal(t, "2026-10-08", late.Key.Day.Format("2006-01-02"))
	assert.Equal(t, time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC), late.Key.DayStart)
	assert.Equal(t, time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), late.Key.DayEnd)

	// 22h30 UTC = 00h30 a Paris le 9 : le compteur du 8 est clos, un autre commence.
	// Avec minuit UTC (ancien comportement) ce message etait encore compte le 8.
	after := reserveAt(t, time.Date(2026, 10, 8, 22, 30, 0, 0, time.UTC))
	assert.Equal(t, "2026-10-09", after.Key.Day.Format("2006-01-02"))
	assert.Equal(t, late.Key.DayEnd, after.Key.DayStart, "aucun trou entre les deux jours")
}

func TestReserveDailyQuota_DaylightSavingDayBoundaries(t *testing.T) {
	spring := reserveAt(t, time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC))
	assert.Equal(t, 23*time.Hour, spring.Key.DayEnd.Sub(spring.Key.DayStart), "29/03/2026 : 23 h")
	autumn := reserveAt(t, time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
	assert.Equal(t, 25*time.Hour, autumn.Key.DayEnd.Sub(autumn.Key.DayStart), "25/10/2026 : 25 h")
	// Hiver (UTC+1) : minuit Paris = 23h00 UTC.
	winter := reserveAt(t, time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC))
	assert.Equal(t, time.Date(2026, 12, 14, 23, 0, 0, 0, time.UTC), winter.Key.DayStart)
}

func TestDailyCapGate_CountsSinceParisMidnight(t *testing.T) {
	withFixedClock(t, time.Date(2026, 10, 8, 22, 30, 0, 0, time.UTC)) // 00h30 le 9 a Paris
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 60)
	ws.Settings.VeridianPerRecipientDailyCap = 2
	provider := parisWindowProvider()
	entry := veridianTestEntryFrom("e", "lead@gmail.com", "hello@envoi.example", domain.EmailQueuePayload{})

	var since time.Time
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "lead@gmail.com", gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, s time.Time) (int, error) { since = s; return 0, nil })

	_, capped := env.worker.veridianDailyCapGate(ws, provider, entry)
	assert.False(t, capped)
	assert.Equal(t, time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), since,
		"le compteur du jour commence a minuit heure de Paris, pas a minuit UTC")
}

func TestEffectivePlan_TodayIsTheParisDay(t *testing.T) {
	ws := veridianTestWorkspace(nil, 60)
	ws.Settings.VeridianMarketingEmailProviderIDs = []string{"int-1"}
	ws.Integrations[0].Type = domain.IntegrationTypeEmail
	ws.Integrations[0].EmailProvider = *parisWindowProvider()
	ws.Integrations[0].EmailProvider.Senders = []domain.EmailSender{{ID: "s", Email: "hello@envoi.example", IsDefault: true}}
	now := time.Date(2026, 10, 8, 22, 30, 0, 0, time.UTC)

	plan := VeridianEffectivePlan(VeridianPlanInput{Workspace: ws, IntegrationID: "int-1", Now: now})

	assert.Equal(t, "2026-10-09", plan.Date, "00h30 a Paris : c'est deja le 9")
	assert.Equal(t, "Europe/Paris", plan.DayTimezone)
	assert.Equal(t, time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), plan.DayStart)
}

// Fusible PAR PROFIL : deux profils du meme domaine emetteur ne melangent plus leurs rejets.
func TestReputationGate_IsCountedPerProfileNotPerSharedDomain(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	const senderDomain = "partage.example"
	repo := env.mockMessageHistoryRepo
	repo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", senderDomain, gomock.Any()).Return(0, nil).AnyTimes()
	repo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws-1", senderDomain, gomock.Any()).Return(100, nil).AnyTimes()
	repo.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws-1", senderDomain, gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ string, _ time.Time) (map[string]domain.VeridianReputationCounts, error) {
			// Le profil "abime" a 30 % de rejets durs, "sain" aucun.
			if domain.VeridianReputationProfileFromContext(ctx) == "abime" {
				return map[string]domain.VeridianReputationCounts{"google": {Sent: 100, HardBounces: 30}}, nil
			}
			return map[string]domain.VeridianReputationCounts{"google": {Sent: 100}}, nil
		}).AnyTimes()

	mk := func(profile string) *domain.EmailQueueEntry {
		e := veridianReputationTestEntryClass("bot@"+senderDomain, "google")
		e.IntegrationID = profile
		return e
	}
	provider := &domain.EmailProvider{}
	abime, sain := mk("abime"), mk("sain")
	_, _ = env.worker.veridianReputationGate(ws, provider, abime)
	_, _ = env.worker.veridianReputationGate(ws, provider, sain)

	assert.Greater(t, env.worker.veridianSlowdownFactor(ws, abime, "google"), 1, "le profil abime est ralenti")
	assert.Equal(t, 1, env.worker.veridianSlowdownFactor(ws, sain, "google"), "le profil sain du meme domaine ne l'est pas")
}
