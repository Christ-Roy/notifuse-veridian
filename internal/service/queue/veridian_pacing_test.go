package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

type fakePacingPlanner struct {
	plan  map[string]VeridianPaceInput
	err   error
	calls int
}

func (f *fakePacingPlanner) ProfilePace(_ context.Context, _ string) (map[string]VeridianPaceInput, error) {
	f.calls++
	return f.plan, f.err
}

// fenêtre ouverte tous les jours, toute la journée, UTC.
func pacingWindowAllDay() *domain.VeridianSendingWindow {
	return &domain.VeridianSendingWindow{Days: []int{0, 1, 2, 3, 4, 5, 6}, StartHour: 0, EndHour: 24, Timezone: "UTC"}
}

func pacingFixture(planner PacingPlanner) (*veridianPacer, *time.Time) {
	clock := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	p := newVeridianPacer(planner, func() time.Time { return clock }, func() float64 { return 0.5 }) // facteur 1 : sans dispersion
	return p, &clock
}

func take(p *veridianPacer, ws *domain.Workspace, prov *domain.EmailProvider, e *domain.EmailQueueEntry, id string) bool {
	ok, _ := p.take(ws, prov, e, id)
	return ok
}

func pacingCommercialEntry() *domain.EmailQueueEntry {
	e := veridianTestEntry("e1", "lead@ovh.fr", domain.EmailQueuePayload{})
	e.SourceType = domain.EmailQueueSourceBroadcast // une campagne (une séquence interrogerait son ancrage)
	return e
}

func TestVeridianPacingInterval(t *testing.T) {
	assert.Equal(t, 10*time.Minute, veridianPacingInterval(10, 110), "110 min pour 10 envois : un toutes les 10 min, le dernier avant la fermeture")
	assert.Equal(t, time.Duration(0), veridianPacingInterval(0, 300), "plus rien d'utile à envoyer : pas d'étalement")
	assert.Equal(t, time.Duration(0), veridianPacingInterval(5, 0), "fenêtre fermée : pas d'étalement (la porte fenêtre tient)")
	assert.Equal(t, veridianPacingMaxInterval, veridianPacingInterval(1, 600), "borné : le dernier envoi reste dans la journée")
	// Le cas mesuré le 27/11 sur greenbeamcraft : 157 restants pour 435 min, soit 2 min 45 s d'écart au lieu de 70 s.
	assert.InDelta(t, (435.0/158)*60, veridianPacingInterval(157, 435).Seconds(), 0.5)
}

func TestVeridianPacer_SpreadsCommercialSendsOverTheWindow(t *testing.T) {
	planner := &fakePacingPlanner{plan: map[string]VeridianPaceInput{"nord": {Remaining: 10, Known: true, MinutesLeft: 110}}}
	p, clock := pacingFixture(planner)
	ws := &domain.Workspace{ID: "ws-1"}
	prov := &domain.EmailProvider{VeridianSendingWindow: pacingWindowAllDay()}
	entry := pacingCommercialEntry()

	v := p.verdict(ws, prov, entry, "nord")
	assert.False(t, v.Blocked(), "premier envoi du processus : il part")
	require.True(t, take(p, ws, prov, entry, "nord"))

	// 30 s après : 9 restants sur 109,5 min -> 10,95 min d'écart, dont 30 s déjà passées.
	*clock = clock.Add(30 * time.Second)
	v = p.verdict(ws, prov, entry, "nord")
	require.True(t, v.Blocked(), "le profil vient d'envoyer : le suivant attend son créneau")
	assert.Equal(t, domain.VeridianReasonPacing, v.Reason)
	assert.Equal(t, domain.VeridianGatePacing, v.Gate)
	assert.InDelta(t, 109.5/10*60-30, v.Delay.Seconds(), 1)
	assert.Equal(t, 1, planner.calls, "le plan est relu au plus toutes les 2 min, pas à chaque examen")
	assert.False(t, take(p, ws, prov, entry, "nord"), "un créneau occupé ne se réserve pas")

	*clock = clock.Add(11 * time.Minute)
	assert.False(t, p.verdict(ws, prov, entry, "nord").Blocked(), "le créneau est passé")
	assert.True(t, take(p, ws, prov, entry, "nord"))
	assert.Equal(t, 2, planner.calls, "plan relu une fois passées les 2 min")
}

func TestVeridianPacer_DispersionKeepsTheDrawnFactorUntilNextSend(t *testing.T) {
	planner := &fakePacingPlanner{plan: map[string]VeridianPaceInput{"nord": {Remaining: 10, Known: true, MinutesLeft: 110}}}
	clock := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	p := newVeridianPacer(planner, func() time.Time { return clock }, func() float64 { return 0.0 }) // facteur 1 - 0,3
	ws := &domain.Workspace{ID: "ws-1"}
	prov := &domain.EmailProvider{VeridianSendingWindow: pacingWindowAllDay()}
	entry := pacingCommercialEntry()
	require.True(t, take(p, ws, prov, entry, "nord"))
	clock = clock.Add(8 * time.Minute) // 11 min x 0,7 = 7 min 42 s
	assert.False(t, p.verdict(ws, prov, entry, "nord").Blocked(), "dispersion -30 % : le créneau s'ouvre plus tôt")
}

func TestVeridianPacer_LetsThroughWhatItDoesNotGovern(t *testing.T) {
	planner := &fakePacingPlanner{plan: map[string]VeridianPaceInput{"nord": {Remaining: 10, Known: true, MinutesLeft: 110}}}
	ws := &domain.Workspace{ID: "ws-1"}
	windowed := &domain.EmailProvider{VeridianSendingWindow: pacingWindowAllDay()}

	t.Run("mail écrit depuis la boîte", func(t *testing.T) {
		p, _ := pacingFixture(planner)
		e := veridianTestEntry("e1", "client@ovh.fr", domain.EmailQueuePayload{})
		e.SourceType = domain.EmailQueueSourceMail
		require.True(t, take(p, ws, windowed, e, "nord"))
		assert.False(t, p.verdict(ws, windowed, e, "nord").Blocked())
	})
	t.Run("profil sans fenêtre", func(t *testing.T) {
		p, _ := pacingFixture(planner)
		e := pacingCommercialEntry()
		require.True(t, take(p, ws, &domain.EmailProvider{}, e, "nord"))
		assert.False(t, p.verdict(ws, &domain.EmailProvider{}, e, "nord").Blocked())
	})
	t.Run("plan illisible", func(t *testing.T) {
		p, _ := pacingFixture(&fakePacingPlanner{err: errors.New("base injoignable")})
		e := pacingCommercialEntry()
		require.True(t, take(p, ws, windowed, e, "nord"))
		assert.False(t, p.verdict(ws, windowed, e, "nord").Blocked(), "une mesure absente ne bloque jamais l'envoi")
	})
	t.Run("capacité illimitée", func(t *testing.T) {
		p, _ := pacingFixture(&fakePacingPlanner{plan: map[string]VeridianPaceInput{"nord": {Known: false, MinutesLeft: 110}}})
		e := pacingCommercialEntry()
		require.True(t, take(p, ws, windowed, e, "nord"))
		assert.False(t, p.verdict(ws, windowed, e, "nord").Blocked())
	})
	t.Run("coupe-circuit", func(t *testing.T) {
		t.Setenv("VERIDIAN_PACING", "off")
		p, _ := pacingFixture(planner)
		e := pacingCommercialEntry()
		require.True(t, take(p, ws, windowed, e, "nord"))
		assert.Equal(t, domain.VeridianVerdictSkipped, p.verdict(ws, windowed, e, "nord").Verdict)
	})
}

// Dans la cascade réelle : le premier mail part par nord, le deuxième bascule sur relai (nord attend son créneau),
// le troisième attend avec la raison « pacing » et le délai du créneau le plus proche.
func TestVeridianSelectSendable_PacingSpreadsAcrossThePool(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	nord.EmailProvider.VeridianSendingWindow = pacingWindowAllDay()
	relai.EmailProvider.VeridianSendingWindow = pacingWindowAllDay()
	ws := veridianTestPoolWorkspace([]string{"nord", "relai"}, []domain.Integration{nord, relai}, nil)
	veridianExpectHealthyReputation(env, "ws-1", "nord-propre.example")
	veridianExpectHealthyReputation(env, "ws-1", "relai-agence.example")

	SetVeridianPacingPlanner(&fakePacingPlanner{plan: map[string]VeridianPaceInput{
		"nord": {Remaining: 10, Known: true, MinutesLeft: 110}, "relai": {Remaining: 10, Known: true, MinutesLeft: 110},
	}})
	t.Cleanup(func() { SetVeridianPacingPlanner(nil) })

	pick := func(id string) veridianSelectionResult {
		e := pacingCommercialEntry()
		e.ID = id
		e.IntegrationID = "nord"
		e.Payload.FromAddress = "hello@nord-propre.example"
		return env.worker.veridianSelectSendableIntegration(ws, e, ws.GetIntegrationByID("nord"))
	}
	s1 := pick("e1")
	require.NotNil(t, s1.Candidate)
	assert.Equal(t, "nord", s1.Candidate.IntegrationID)
	s2 := pick("e2")
	require.NotNil(t, s2.Candidate)
	assert.Equal(t, "relai", s2.Candidate.IntegrationID, "nord vient d'envoyer : le pool passe la main à relai")
	s3 := pick("e3")
	assert.Nil(t, s3.Candidate, "les deux profils viennent d'envoyer : le troisième attend")
	assert.False(t, s3.Permanent)
	assert.Equal(t, domain.VeridianReasonPacing, s3.Reason)
	// 9 restants sur 110 min = 11 min, dispersé de ±30 % : entre 7 min 42 s et 14 min 18 s.
	assert.GreaterOrEqual(t, s3.RetryDelay, time.Duration(0.7*float64(11*time.Minute))-time.Second)
	assert.LessOrEqual(t, s3.RetryDelay, time.Duration(1.3*float64(11*time.Minute)))
}

// 27/11, mesuré en prod : une entrée retenue puis refusée plus loin (réservation atomique du quota, pré-filtre, garde
// finale, refus avant acceptation) consommait un créneau de 4 min sans rien envoyer ; greenbeamcraft n'envoyait plus.
// Le créneau est rendu, sauf si un envoi suivant l'a déjà pris.
func TestVeridianPacer_UndoGivesTheSlotBack(t *testing.T) {
	planner := &fakePacingPlanner{plan: map[string]VeridianPaceInput{"nord": {Remaining: 10, Known: true, MinutesLeft: 110}}}
	p, clock := pacingFixture(planner)
	ws := &domain.Workspace{ID: "ws-1"}
	prov := &domain.EmailProvider{VeridianSendingWindow: pacingWindowAllDay()}
	entry := pacingCommercialEntry()

	ok, first := p.take(ws, prov, entry, "nord")
	require.True(t, ok)
	require.NotNil(t, first)
	*clock = clock.Add(12 * time.Minute)
	ok, undo := p.take(ws, prov, entry, "nord")
	require.True(t, ok)
	*clock = clock.Add(time.Second)
	require.True(t, p.verdict(ws, prov, entry, "nord").Blocked(), "créneau pris")

	before := p.sincePlan[veridianPacingKey("ws-1", "nord")]
	undo() // l'entrée n'est pas partie
	assert.False(t, p.verdict(ws, prov, entry, "nord").Blocked(), "créneau rendu : le premier envoi date de 12 min")
	assert.Equal(t, before-1, p.sincePlan[veridianPacingKey("ws-1", "nord")], "l'envoi annulé ne compte plus")

	ok, _ = p.take(ws, prov, entry, "nord")
	require.True(t, ok)
	first() // trop tard : un envoi suivant a pris le créneau depuis
	assert.True(t, p.verdict(ws, prov, entry, "nord").Blocked(), "une annulation périmée ne libère rien")
}
