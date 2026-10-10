package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/emailerror"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fiche 62, lot 1 : toutes les portes sont evaluees pour EXPLIQUER la decision, sans
// la changer. Ces tests prouvent (1) que la vraie raison n'est plus masquee, (2) que
// le gagnant est IDENTIQUE a celui de l'ancienne boucle sur toute une matrice de
// situations, (3) qu'expliquer ne consomme pas de jeton de debit.

// parityHistory : faux historique deterministe pour la matrice de parite. Un domaine
// « plafonne » rend 1 envoi du jour (plafond de classe = 1) mais 0 pour l'amorcage du
// limiter (fenetre recente), comme une journee ou le debit a eu le temps de revenir.
type parityHistory struct {
	domain.MessageHistoryRepository
	capped map[string]bool
}

func (h *parityHistory) CountComplainedSinceForSenderDomain(context.Context, string, string, time.Time) (int, error) {
	return 0, nil
}

func (h *parityHistory) CountSentSinceForSenderDomain(context.Context, string, string, time.Time) (int, error) {
	return 0, nil
}

func (h *parityHistory) CountSentSinceForClassAndSenderDomain(_ context.Context, _ string, _ string, senderDomain string, since time.Time) (int, error) {
	if h.capped[senderDomain] && time.Since(since) > 2*time.Hour {
		return 1, nil
	}
	return 0, nil
}

// closedWindow : une fenetre dont le jour d'aujourd'hui est exclu, donc fermee quelle
// que soit l'heure du test.
func closedWindow() *domain.VeridianSendingWindow {
	return &domain.VeridianSendingWindow{
		Days:      []int{int(time.Now().UTC().Weekday()+3) % 7},
		StartHour: 8, EndHour: 19, Timezone: "UTC",
	}
}

type paritySituation struct {
	windowClosed map[string]bool
	capped       map[string]bool
	throttled    map[string]bool
}

func (s paritySituation) setup(t *testing.T) (*veridianThrottleTestEnv, *domain.Workspace, *domain.EmailQueueEntry) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	if s.windowClosed["nord"] {
		nord.EmailProvider.VeridianSendingWindow = closedWindow()
	}
	if s.windowClosed["relai"] {
		relai.EmailProvider.VeridianSendingWindow = closedWindow()
	}
	ws := veridianTestPoolWorkspace([]string{"nord", "relai"}, []domain.Integration{nord, relai}, map[string]int{"google": 1})
	ws.Settings.VeridianProviderClassRates = map[string]float64{"google": 0.5}
	env.worker.messageHistoryRepo = &parityHistory{capped: map[string]bool{
		"nord-propre.example":  s.capped["nord"],
		"relai-agence.example": s.capped["relai"],
	}}
	for _, id := range []string{"nord", "relai"} {
		if s.throttled[id] {
			require.True(t, env.worker.providerClassLimiter.Allow(id, "google", 0.5)) // vide le jeton de burst
		}
	}
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example"})
	entry.IntegrationID = "nord"
	return env, ws, entry
}

// legacyWinner rejoue l'ANCIENNE boucle de selection (court-circuit, ordre historique
// exclusion, reputation, debit, plafond, plafond adresse, fenetre) avec les fonctions
// historiques (wrappers des variantes Verdict).
func legacyWinner(w *EmailQueueWorker, ws *domain.Workspace, entry *domain.EmailQueueEntry, assigned *domain.Integration) string {
	cands := w.veridianBuildFailoverCandidates(ws, entry, assigned)
	savedFrom, savedName, savedID := entry.Payload.FromAddress, entry.Payload.FromName, entry.IntegrationID
	defer func() {
		entry.Payload.FromAddress, entry.Payload.FromName, entry.IntegrationID = savedFrom, savedName, savedID
	}()
	for _, cand := range cands {
		if cand.Provider == nil || cand.Provider.VeridianPaused {
			continue
		}
		if cand.IntegrationID != entry.IntegrationID && cand.FromAddress == "" {
			continue
		}
		entry.Payload.FromAddress, entry.Payload.FromName, entry.IntegrationID = cand.FromAddress, cand.FromName, cand.IntegrationID
		if _, ex := w.veridianExcludedClassGate(ws, cand.Provider, entry); ex {
			continue
		}
		if _, b := w.veridianReputationGate(ws, cand.Provider, entry); b {
			continue
		}
		if _, b := w.veridianProviderClassGate(ws, cand.Provider, entry); b {
			continue
		}
		if _, b := w.veridianDailyCapGate(ws, cand.Provider, entry); b {
			continue
		}
		if _, b := w.veridianPerSenderCapGate(ws, cand.Provider, entry); b {
			continue
		}
		if _, b := w.veridianSendingWindowGate(ws, cand.Provider, entry); b {
			continue
		}
		return cand.IntegrationID
	}
	return ""
}

// TestVeridianSelect_ParityWithLegacyLoop : sur les 64 situations (fenetre fermee,
// plafond atteint, debit epuise, pour chacun des deux profils), le gagnant de la
// selection qui evalue TOUT est le meme que celui de l'ancienne boucle.
func TestVeridianSelect_ParityWithLegacyLoop(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		bit := func(i int) bool { return mask&(1<<i) != 0 }
		s := paritySituation{
			windowClosed: map[string]bool{"nord": bit(0), "relai": bit(1)},
			capped:       map[string]bool{"nord": bit(2), "relai": bit(3)},
			throttled:    map[string]bool{"nord": bit(4), "relai": bit(5)},
		}
		t.Run(fmt.Sprintf("mask_%02d", mask), func(t *testing.T) {
			envNew, wsNew, entryNew := s.setup(t)
			got := envNew.worker.veridianSelectSendableIntegration(wsNew, entryNew, wsNew.GetIntegrationByID("nord"))

			envOld, wsOld, entryOld := s.setup(t)
			want := legacyWinner(envOld.worker, wsOld, entryOld, wsOld.GetIntegrationByID("nord"))

			gotID := ""
			if got.Candidate != nil {
				gotID = got.Candidate.IntegrationID
			}
			assert.Equal(t, want, gotID, "le gagnant ne doit pas changer")
			if gotID == "" {
				assert.NotEmpty(t, got.Reason, "sans gagnant, une raison est toujours donnee")
				assert.Positive(t, got.RetryDelay)
			}
			assert.Equal(t, "nord", entryNew.IntegrationID, "l'entree est restauree comme avant")
		})
	}
}

// TestVeridianSelect_SaturdayCase_WindowClosedNoLongerMaskedByClassRate rejoue le cas
// mesure en prod le samedi 10/10 : fenetre fermee ET debit de classe epuise. L'ancien
// code rendait 5 min (le debit masquait la fenetre) et re-examinait 1 200 entrees toutes
// les 5 minutes tout le week-end ; la raison est desormais la fenetre, avec sa reouverture.
func TestVeridianSelect_SaturdayCase_WindowClosedNoLongerMaskedByClassRate(t *testing.T) {
	s := paritySituation{
		windowClosed: map[string]bool{"nord": true, "relai": true},
		throttled:    map[string]bool{"nord": true, "relai": true},
	}
	env, ws, entry := s.setup(t)
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))

	require.Nil(t, sel.Candidate)
	assert.Equal(t, domain.VeridianReasonWindowClosed, sel.Reason, "la vraie raison, pas le premier refus")
	assert.Greater(t, sel.RetryDelay, time.Hour, "pas de re-examen avant la reouverture (l'ancien code : 5 min)")
	assert.LessOrEqual(t, sel.RetryDelay, veridianSendingWindowMaxRetryDelay)

	// La trace garde TOUS les refus avec valeur, limite et verdict.
	require.Len(t, sel.Evaluations, 2)
	byGate := map[string]veridianGateVerdict{}
	for _, g := range sel.Evaluations[0].Gates {
		byGate[g.Gate] = g
	}
	assert.True(t, byGate[domain.VeridianGateClassRate].Blocked(), "le debit de classe a bien refuse aussi")
	win := byGate[domain.VeridianGateWindow]
	assert.True(t, win.Blocked())
	assert.NotEmpty(t, win.Value)
	assert.Contains(t, win.Limit, "08:00-19:00")
	assert.Contains(t, win.Detail, "reopens=")
}

// TestVeridianSelect_ExplainingDoesNotConsumeAClassToken : un jeton de debit n'est
// consomme que si l'entree part. Quand une autre porte bloque, le limiter n'est ni
// debite ni amorce (sinon l'entree suivante, qui partirait, serait privee de son jeton).
func TestVeridianSelect_ExplainingDoesNotConsumeAClassToken(t *testing.T) {
	env, ws, entry := paritySituation{windowClosed: map[string]bool{"nord": true, "relai": true}}.setup(t)
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))
	require.Nil(t, sel.Candidate)
	assert.Empty(t, env.worker.GetProviderClassStats(), "fenetre fermee : le limiter de debit n'a pas ete touche")

	// Tout ouvert : le jeton est consomme, une seule fois, par le gagnant.
	env2, ws2, entry2 := paritySituation{}.setup(t)
	sel2 := env2.worker.veridianSelectSendableIntegration(ws2, entry2, ws2.GetIntegrationByID("nord"))
	require.NotNil(t, sel2.Candidate)
	assert.Equal(t, "nord", sel2.Candidate.IntegrationID)
	stats := env2.worker.GetProviderClassStats()
	require.Contains(t, stats, "nord|google")
	assert.Less(t, stats["nord|google"].TokensAvailable, 1.0, "le gagnant a consomme son jeton")
	assert.NotContains(t, stats, "relai|google")
}

func TestVeridianSelect_CapacityTraceCarriesValueLimitAndGateName(t *testing.T) {
	s := paritySituation{capped: map[string]bool{"nord": true, "relai": true}}
	env, ws, entry := s.setup(t)
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))
	require.Nil(t, sel.Candidate)
	assert.Equal(t, domain.VeridianReasonCapacity, sel.Reason)
	assert.Equal(t, "provider_class", sel.ReasonDetail)
	var daily veridianGateVerdict
	for _, g := range sel.Evaluations[0].Gates {
		if g.Gate == domain.VeridianGateDailyCap {
			daily = g
		}
	}
	assert.Equal(t, 1, daily.Value)
	assert.Equal(t, 1, daily.Limit)
	assert.Equal(t, "provider_class", daily.Name)
	assert.Equal(t, veridianDailyCapRecheckInterval, sel.RetryDelay)
}

func TestVeridianSelect_AnchoredFollowUpWaitingOnItsSender_IsAnchorWait(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	ws := veridianTestPoolWorkspace([]string{"nord", "relai"}, []domain.Integration{nord, relai}, map[string]int{"google": 1})
	entry := veridianTestAutomationEntry("relance1", "automation-1", "lead@gmail.com", "hello@nord-propre.example")

	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@gmail.com", gomock.Any(), 0).
		Return(veridianTestSequenceHistory("automation-1", "lead@gmail.com", "nord", "hello@nord-propre.example", time.Now().Add(-4*24*time.Hour)), 1, nil)
	env.mockMessageHistoryRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "nord-propre.example", gomock.Any()).Return(1, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "ws-1", "nord-propre.example", gomock.Any()).Return(0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().ReputationCountsByClassSinceForSenderDomain(gomock.Any(), "ws-1", "nord-propre.example", gomock.Any()).
		Return(map[string]domain.VeridianReputationCounts{}, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "nord-propre.example", gomock.Any()).Return(1, nil)

	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))
	require.Nil(t, sel.Candidate)
	assert.Equal(t, domain.VeridianReasonAnchorWait, sel.Reason)
	assert.Equal(t, domain.VeridianReasonCapacity, sel.ReasonDetail)
	assert.True(t, sel.Anchor.Only)
}

func TestVeridianSelect_NoReachableCandidateReasons(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := planPauseWorkspace("nord", "relai")
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{FromAddress: "hello@nord-propre.example"})
	entry.IntegrationID = "nord"
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))
	assert.Equal(t, domain.VeridianReasonProfilePaused, sel.Reason)

	ws2 := planPauseWorkspace()
	for i := 0; i < 5; i++ {
		env.worker.circuitBreaker.RecordFailure("nord", providerError())
		env.worker.circuitBreaker.RecordFailure("relai", providerError())
	}
	sel2 := env.worker.veridianSelectSendableIntegration(ws2, entry, ws2.GetIntegrationByID("nord"))
	assert.Equal(t, domain.VeridianReasonCircuitOpen, sel2.Reason)
	assert.LessOrEqual(t, sel2.RetryDelay, env.worker.circuitBreaker.GetConfig().CooldownPeriod)
}

func TestVeridianDominantBlock_LongestDelayWins_LaterGateOnTie(t *testing.T) {
	gates := []veridianGateVerdict{
		{Gate: domain.VeridianGateClassRate, Verdict: domain.VeridianVerdictBlock, Delay: 5 * time.Minute, Reason: domain.VeridianReasonClassRate},
		{Gate: domain.VeridianGateDailyCap, Verdict: domain.VeridianVerdictPass},
		{Gate: domain.VeridianGateWindow, Verdict: domain.VeridianVerdictBlock, Delay: 20 * time.Hour, Reason: domain.VeridianReasonWindowClosed},
	}
	d, longest := veridianDominantBlock(gates)
	require.NotNil(t, d)
	assert.Equal(t, domain.VeridianReasonWindowClosed, d.Reason)
	assert.Equal(t, 20*time.Hour, longest)

	tie := []veridianGateVerdict{
		{Gate: domain.VeridianGateClassRate, Verdict: domain.VeridianVerdictBlock, Delay: time.Hour, Reason: domain.VeridianReasonClassRate},
		{Gate: domain.VeridianGateDailyCap, Verdict: domain.VeridianVerdictBlock, Delay: time.Hour, Reason: domain.VeridianReasonCapacity},
	}
	d, _ = veridianDominantBlock(tie)
	assert.Equal(t, domain.VeridianReasonCapacity, d.Reason)

	none, zero := veridianDominantBlock([]veridianGateVerdict{{Verdict: domain.VeridianVerdictPass}})
	assert.Nil(t, none)
	assert.Zero(t, zero)
}

func TestProviderClassRateLimiter_PeekSeededNeverConsumesNorCreates(t *testing.T) {
	prl := NewProviderClassRateLimiter()
	assert.True(t, prl.PeekSeeded("i", "google", 0.5, nil))
	assert.Empty(t, prl.GetStats(), "peek ne cree pas le limiter")
	assert.False(t, prl.PeekSeeded("i", "google", 0.5, func() bool { return true }), "un envoi recent bloque, comme AllowSeeded")
	assert.Empty(t, prl.GetStats())

	assert.True(t, prl.Allow("i", "google", 0.5))
	assert.False(t, prl.PeekSeeded("i", "google", 0.5, nil), "jeton consomme")
	assert.False(t, prl.PeekSeeded("i", "google", 0.5, nil), "peek repete : rien ne change")
}

func TestVeridianSendingWindowVerdict_DescribesWindowAndReopening(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	prov := &domain.EmailProvider{VeridianSendingWindow: closedWindow()}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
	v := env.worker.veridianSendingWindowVerdict(ws, prov, entry)
	assert.True(t, v.Blocked())
	assert.Equal(t, domain.VeridianReasonWindowClosed, v.Reason)
	assert.Equal(t, domain.VeridianGateWindow, v.Gate)
	assert.Contains(t, v.Limit, "08:00-19:00 UTC")
	delay, closed := env.worker.veridianSendingWindowGate(ws, prov, entry)
	assert.True(t, closed)
	assert.InDelta(t, v.Delay.Seconds(), delay.Seconds(), 2, "le wrapper rend le meme delai que le verdict")
}

func providerError() *emailerror.ClassifiedError {
	return &emailerror.ClassifiedError{Type: emailerror.ErrorTypeProvider}
}
