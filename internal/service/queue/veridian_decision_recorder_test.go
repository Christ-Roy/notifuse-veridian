package queue

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deferralQueueRepo : depot de file de test qui sait aussi porter la RAISON d'un
// report (interface optionnelle domain.EmailQueueDeferralRepository).
type deferralQueueRepo struct {
	*mocks.MockEmailQueueRepository
	mu  sync.Mutex
	got []domain.EmailQueueDeferral
	err error
}

func (r *deferralQueueRepo) SetDeferral(_ context.Context, _ string, _ string, d domain.EmailQueueDeferral) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.got = append(r.got, d)
	return nil
}

type fakeDecisionLog struct {
	mu   sync.Mutex
	rows []*domain.VeridianSendDecision
	err  error
}

func (l *fakeDecisionLog) Insert(_ context.Context, _ string, d *domain.VeridianSendDecision) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.rows = append(l.rows, d)
	return nil
}

func (l *fakeDecisionLog) List(context.Context, string, domain.VeridianSendDecisionFilter) ([]*domain.VeridianSendDecision, string, error) {
	return l.rows, "", nil
}

func recorderEnv(t *testing.T) (*veridianThrottleTestEnv, *deferralQueueRepo, *fakeDecisionLog) {
	env := newVeridianThrottleTestEnv(t)
	repo := &deferralQueueRepo{MockEmailQueueRepository: env.mockQueueRepo}
	env.worker.queueRepo = repo
	log := &fakeDecisionLog{}
	env.worker.SetDecisionLog(log)
	return env, repo, log
}

// TestProcessEntry_SaturdayWindowClosed_PersistsTheRealReasonAndSleepsUntilOpening est
// le test de non-regression du « report en boucle » : avant, l'entree etait re-planifiee
// dans 5 minutes sans aucune raison.
func TestProcessEntry_SaturdayWindowClosed_PersistsTheRealReasonAndSleepsUntilOpening(t *testing.T) {
	s := paritySituation{
		windowClosed: map[string]bool{"nord": true, "relai": true},
		throttled:    map[string]bool{"nord": true, "relai": true},
	}
	env, ws, entry := s.setup(t)
	repo := &deferralQueueRepo{MockEmailQueueRepository: env.mockQueueRepo}
	env.worker.queueRepo = repo
	log := &fakeDecisionLog{}
	env.worker.SetDecisionLog(log)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(ws, entry)

	require.Len(t, repo.got, 1)
	d := repo.got[0]
	assert.Equal(t, domain.VeridianReasonWindowClosed, d.Reason)
	assert.Greater(t, time.Until(d.Until), time.Hour, "reporte jusqu'a la reouverture, pas de 5 minutes")
	assert.True(t, d.Logged)

	require.Len(t, log.rows, 1)
	row := log.rows[0]
	assert.Equal(t, domain.VeridianOutcomeDeferred, row.Outcome)
	assert.Equal(t, domain.VeridianReasonWindowClosed, row.Reason)
	require.NotNil(t, row.Trace)
	require.NotEmpty(t, row.Trace.Candidates)
	gates := map[string]domain.VeridianGateRecord{}
	for _, g := range row.Trace.Candidates[0].Gates {
		gates[g.Gate] = g
	}
	assert.Equal(t, domain.VeridianVerdictBlock, gates[domain.VeridianGateWindow].Verdict)
	assert.Equal(t, domain.VeridianVerdictBlock, gates[domain.VeridianGateClassRate].Verdict)
	assert.Equal(t, "block", gates[domain.VeridianGateWindow].Verdict)
	assert.Equal(t, "deferred", row.Trace.Decision.Outcome)
}

func TestVeridianShouldLogDeferral_TransitionsPolicy(t *testing.T) {
	now := time.Now()
	recent := now.Add(-time.Hour)
	old := now.Add(-25 * time.Hour)
	d := veridianDeferral{Reason: domain.VeridianReasonWindowClosed, Profile: "nord"}
	same := func() *domain.EmailQueueEntry {
		return &domain.EmailQueueEntry{DeferReason: "window_closed", DeferProfile: "nord", DeferCount: 3, DecisionLoggedAt: &recent}
	}

	w, sampled, reduced := veridianShouldLogDeferral(domain.VeridianDecisionLogTransitions, &domain.EmailQueueEntry{}, d, now)
	assert.True(t, w, "premiere decision d'une entree")
	assert.False(t, sampled)
	assert.False(t, reduced)

	w, _, _ = veridianShouldLogDeferral(domain.VeridianDecisionLogTransitions, same(), d, now)
	assert.False(t, w, "meme raison, meme profil, battement recent : rien")

	e := same()
	e.DeferReason = "class_rate"
	w, _, reduced = veridianShouldLogDeferral(domain.VeridianDecisionLogTransitions, e, d, now)
	assert.True(t, w, "changement de raison")
	assert.False(t, reduced)

	e = same()
	e.DeferProfile = "relai"
	w, _, _ = veridianShouldLogDeferral(domain.VeridianDecisionLogTransitions, e, d, now)
	assert.True(t, w, "changement de profil candidat")

	e = same()
	e.DecisionLoggedAt = &old
	w, sampled, reduced = veridianShouldLogDeferral(domain.VeridianDecisionLogTransitions, e, d, now)
	assert.True(t, w, "battement de 24 h")
	assert.False(t, sampled)
	assert.True(t, reduced, "le battement garde une trace reduite")

	e = same()
	e.DeferCount = veridianDecisionSampleEvery - 1
	w, sampled, reduced = veridianShouldLogDeferral(domain.VeridianDecisionLogTransitions, e, d, now)
	assert.True(t, w, "1 examen sur 200")
	assert.True(t, sampled)
	assert.False(t, reduced, "l'echantillon est en trace complete")

	w, _, _ = veridianShouldLogDeferral(domain.VeridianDecisionLogOff, &domain.EmailQueueEntry{}, d, now)
	assert.False(t, w, "niveau off : jamais")
	w, _, _ = veridianShouldLogDeferral(domain.VeridianDecisionLogAll, same(), d, now)
	assert.True(t, w, "niveau all : chaque examen")
}

func TestVeridianDeferEntry_LevelOff_StillPersistsTheReasonButWritesNoRow(t *testing.T) {
	env, repo, log := recorderEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.VeridianDecisionLogLevel = domain.VeridianDecisionLogOff
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	env.worker.veridianDeferEntry(ws, entry, veridianDeferral{Reason: domain.VeridianReasonCapacity, Detail: "warmup", Delay: time.Hour}, nil, nil)

	require.Len(t, repo.got, 1)
	assert.Equal(t, "capacity", repo.got[0].Reason)
	assert.False(t, repo.got[0].Logged)
	assert.Empty(t, log.rows)
}

func TestVeridianDeferEntry_FallsBackToAPlainRescheduleWhenTheReasonCannotBeStored(t *testing.T) {
	env, repo, _ := recorderEnv(t)
	repo.err = errors.New("column does not exist")
	ws := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
	called := false
	env.worker.veridianDeferEntry(ws, entry, veridianDeferral{Reason: domain.VeridianReasonClassRate, Delay: time.Minute}, nil, func() error {
		called = true
		return nil
	})
	assert.True(t, called, "l'entree doit TOUJOURS etre re-planifiee, sinon elle serait re-examinee en boucle")
}

func TestVeridianDeferEntry_RepoWithoutDeferralSupport_UsesLegacyReschedule(t *testing.T) {
	env := newVeridianThrottleTestEnv(t) // MockEmailQueueRepository n'implemente pas SetDeferral
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
	called := false
	env.worker.veridianDeferEntry(veridianTestWorkspace(nil, 6000), entry, veridianDeferral{Reason: domain.VeridianReasonClassRate, Delay: time.Minute}, nil, func() error {
		called = true
		return nil
	})
	assert.True(t, called)
}

func TestVeridianRecordTerminal_SentCarriesTheGatesThatLetItThrough(t *testing.T) {
	env, ws, entry := paritySituation{}.setup(t)
	log := &fakeDecisionLog{}
	env.worker.SetDecisionLog(log)
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))
	require.NotNil(t, sel.Candidate)

	env.worker.veridianRecordTerminal(ws, entry, domain.VeridianOutcomeSent, "", "", "nord", &sel)

	require.Len(t, log.rows, 1)
	row := log.rows[0]
	assert.Equal(t, domain.VeridianOutcomeSent, row.Outcome)
	assert.Equal(t, "nord", row.ProfileID)
	require.NotNil(t, row.Trace)
	assert.Equal(t, "selected", row.Trace.Candidates[0].Outcome)
	for _, g := range row.Trace.Candidates[0].Gates {
		assert.NotEqual(t, domain.VeridianVerdictBlock, g.Verdict, "aucune porte ne bloque un envoi accepte: %s", g.Gate)
	}
}

func TestVeridianBuildTrace_ReducedKeepsOnlyBlockingGates(t *testing.T) {
	s := paritySituation{windowClosed: map[string]bool{"nord": true, "relai": true}}
	env, ws, entry := s.setup(t)
	sel := env.worker.veridianSelectSendableIntegration(ws, entry, ws.GetIntegrationByID("nord"))
	full := env.worker.veridianBuildTrace(ws, entry, &sel, domain.VeridianTraceDecision{Outcome: "deferred"}, false)
	reduced := env.worker.veridianBuildTrace(ws, entry, &sel, domain.VeridianTraceDecision{Outcome: "deferred"}, true)
	assert.Equal(t, "full", full.Level)
	assert.Equal(t, "reduced", reduced.Level)
	assert.Greater(t, len(full.Candidates[0].Gates), len(reduced.Candidates[0].Gates))
	for _, g := range reduced.Candidates[0].Gates {
		assert.Equal(t, domain.VeridianVerdictBlock, g.Verdict)
	}
}

func TestVeridianFailureReason(t *testing.T) {
	assert.Equal(t, domain.VeridianReasonExcludedClass, veridianFailureReason(errors.New("excluded_provider_class:ionos")))
	assert.Equal(t, "invalid_recipient", veridianFailureReason(errors.New("pre-filtered recipient: undeliverable_domain")))
	assert.Equal(t, domain.VeridianReasonRenderFailed, veridianFailureReason(errors.New("render_at_send: le sujet rendu est vide")))
	assert.Equal(t, domain.VeridianReasonSendError, veridianFailureReason(errors.New("550 user unknown")))
	assert.Equal(t, domain.VeridianReasonSendError, veridianFailureReason(nil))
}

func TestVeridianTrimDetail_NeverCutsAMultibyteCharacter(t *testing.T) {
	long := strings.Repeat("é", 200)
	out := veridianTrimDetail(long)
	assert.LessOrEqual(t, len(out), veridianDecisionDetailMax)
	assert.True(t, strings.HasPrefix(long, out))
	assert.Equal(t, "court", veridianTrimDetail("court"))
}

func TestVeridianInsertDecision_ErrorIsSwallowed(t *testing.T) {
	env, _, log := recorderEnv(t)
	log.err = errors.New("db down")
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
	// Ne panique pas, ne bloque pas : le journal est best-effort.
	env.worker.veridianRecordTerminal(veridianTestWorkspace(nil, 6000), entry, domain.VeridianOutcomeFailed, "send_error", "boom", "int-1", nil)
	assert.Empty(t, log.rows)
}
