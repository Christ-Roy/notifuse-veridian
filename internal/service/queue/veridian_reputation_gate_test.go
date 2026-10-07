package queue

import (
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// Fusible de réputation (correctif 2026-09-29, mission "fusibles natifs de
// réputation") : ces tests PROVOQUENT chaque seuil pour prouver que le gate
// fige réellement l'infra, et prouvent aussi la non-régression (infra saine,
// pas de FROM exploitable) pour ne jamais bloquer plus qu'il ne faut.

func veridianReputationTestEntry(fromAddress string) *domain.EmailQueueEntry {
	return veridianTestEntry("rep-e1", "victim@example.com", domain.EmailQueuePayload{
		FromAddress: fromAddress,
	})
}

func TestVeridianReputationGate_NoSenderDomainIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("") // pas de FROM exploitable

	// Aucune attribution d'infra possible : aucun COUNT appelé (ctrl.Finish
	// refuserait un appel non déclaré), jamais gelé.
	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.False(t, frozen)
	assert.Zero(t, delay)
}

func TestVeridianReputationGate_HealthyInfraNotFrozen(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")

	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(100, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(2, nil) // 2/100 = 2% < 3%

	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.False(t, frozen, "2%% de bounce dur est SOUS le seuil de 3%%, ne doit pas geler")
	assert.Zero(t, delay)
}

func TestVeridianReputationGate_HardBounceRateAtThresholdFreezes(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")

	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(100, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(3, nil) // 3/100 = exactement 3% → PROVOQUE le seuil

	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.True(t, frozen, "3%% de bounce dur ATTEINT le seuil, doit geler l'infra")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianReputationGate_HardBounceRateAboveThresholdFreezes(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("r.brunon@agence-veridian.fr")

	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "agence-veridian.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "agence-veridian.fr", gomock.Any()).
		Return(30, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws-1", "agence-veridian.fr", gomock.Any()).
		Return(5, nil) // 5/30 = 16.7% >> 3%

	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.True(t, frozen)
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianReputationGate_SingleComplaintFreezesImmediately(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")

	// Une SEULE plainte suffit : le gate n'a même pas besoin d'interroger le
	// volume envoyé ni les bounces (court-circuit avant), contrairement au
	// bounce dur qui a besoin d'un dénominateur.
	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(1, nil)

	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.True(t, frozen, "une seule plainte doit geler l'infra, sans seuil")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianReputationGate_NoVolumeYetIsNotFrozen(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")

	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(0, nil) // aucun envoi cette semaine : aucun taux calculable

	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.False(t, frozen)
	assert.Zero(t, delay)
}

func TestVeridianReputationGate_DBErrorFailsClosed(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")

	// Un fusible de réputation qui devient silencieux sous erreur DB n'est pas
	// un fusible : il doit bloquer, pas laisser passer.
	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "messagerie-nord-776.fr", gomock.Any()).
		Return(0, errors.New("db down"))

	delay, frozen := env.worker.veridianReputationGate(ws, nil, entry)
	assert.True(t, frozen, "une erreur DB doit fermer le fusible (fail closed), jamais l'ouvrir")
	assert.Equal(t, veridianDailyCapRecheckInterval, delay)
}

func TestVeridianComputeReputationStatus_MatchesGateDecision(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)

	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "agence-veridian.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "agence-veridian.fr", gomock.Any()).
		Return(30, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws-1", "agence-veridian.fr", gomock.Any()).
		Return(5, nil)

	status, err := VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr", nil, time.Now())
	assert.NoError(t, err)
	assert.True(t, status.Frozen)
	assert.Equal(t, "hard_bounce_rate", status.FrozenReason)
	assert.InDelta(t, 0.1667, status.HardBounceRate, 0.001)
	assert.Equal(t, 7, status.WindowDays)
}

// Seuil par profil (decision 2026-10-07) : un MEME taux de 6% gele au defaut 3%
// et ne gele pas a 8%. La plainte, elle, gele toujours.
func veridianExpectRate(env *veridianThrottleTestEnv, domainName string, complaints, sent, hard int) {
	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(complaints, nil)
	if complaints > 0 {
		return
	}
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(sent, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountHardBouncedSinceForSenderDomain(gomock.Any(), "ws-1", domainName, gomock.Any()).Return(hard, nil)
}

func TestVeridianReputationGate_SameRate6pct_FrozenAtDefault3_NotAtProfile8(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")

	// 6/100 = 6%, profil sans seuil : defaut 3% -> gele.
	env := newVeridianThrottleTestEnv(t)
	veridianExpectRate(env, "messagerie-nord-776.fr", 0, 100, 6)
	_, frozen := env.worker.veridianReputationGate(ws, &domain.EmailProvider{}, entry)
	assert.True(t, frozen, "6%% doit geler au seuil par defaut de 3%%")

	// Meme taux, profil a 0.08 -> libre.
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRate(env, "messagerie-nord-776.fr", 0, 100, 6)
	_, frozen = env.worker.veridianReputationGate(ws, &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, entry)
	assert.False(t, frozen, "6%% ne doit PAS geler un profil a 8%%")

	// A 8% pile, le fusible reste atteint (>=).
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRate(env, "messagerie-nord-776.fr", 0, 100, 8)
	_, frozen = env.worker.veridianReputationGate(ws, &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, entry)
	assert.True(t, frozen, "8%% atteint le seuil 8%%")

	// 16% (agences en chauffe) reste gele a 8%.
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRate(env, "messagerie-nord-776.fr", 0, 31, 5)
	_, frozen = env.worker.veridianReputationGate(ws, &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, entry)
	assert.True(t, frozen, "16%% doit rester gele a 8%%")
}

func TestVeridianReputationGate_ComplaintFreezesEvenWithRelaxedThreshold(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := &domain.Workspace{ID: "ws-1"}
	entry := veridianReputationTestEntry("robert@messagerie-nord-776.fr")
	veridianExpectRate(env, "messagerie-nord-776.fr", 1, 0, 0)
	_, frozen := env.worker.veridianReputationGate(ws, &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.15}, entry)
	assert.True(t, frozen, "une plainte gele toujours, quel que soit le seuil bounce")
}

func TestVeridianComputeReputationStatus_ExposesEffectiveThreshold(t *testing.T) {
	// 6% au defaut -> gele, seuil expose 0.03 non custom.
	env := newVeridianThrottleTestEnv(t)
	veridianExpectRate(env, "agence-veridian.fr", 0, 216, 13)
	st, err := VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr", &domain.EmailProvider{}, time.Now())
	assert.NoError(t, err)
	assert.True(t, st.Frozen)
	assert.InDelta(t, 0.03, st.Threshold, 1e-9)
	assert.False(t, st.ThresholdCustom)

	// Meme mesure, profil a 0.08 -> libre, seuil custom expose.
	env = newVeridianThrottleTestEnv(t)
	veridianExpectRate(env, "agence-veridian.fr", 0, 216, 13)
	st, err = VeridianComputeReputationStatus(env.worker.ctx, env.mockMessageHistoryRepo, "ws-1", "agence-veridian.fr", &domain.EmailProvider{VeridianHardBounceFreezeThreshold: 0.08}, time.Now())
	assert.NoError(t, err)
	assert.False(t, st.Frozen)
	assert.InDelta(t, 0.08, st.Threshold, 1e-9)
	assert.True(t, st.ThresholdCustom)
}
