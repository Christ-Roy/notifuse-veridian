package queue

import (
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// windowAround construit une fenêtre [now+start, now+end] (UTC) pour des tests
// indépendants de l'heure de CI : windowAround(-1h,+1h) est OUVERTE maintenant,
// windowAround(+2h,+3h) est FERMÉE. Days vide = tous les jours (le jour est
// couvert par les tests domain). La fenêtre est exprimée en heure-de-la-journée :
// elle n'a pas de sens si la plage enjambe minuit (les bornes s'inversent). On
// laisse l'appelant skip via skipNearMidnight quand c'est le cas.
func windowAround(start, end time.Duration) *domain.VeridianSendingWindow {
	now := time.Now().UTC()
	s := now.Add(start)
	e := now.Add(end)
	return &domain.VeridianSendingWindow{
		StartHour:   s.Hour(),
		StartMinute: s.Minute(),
		EndHour:     e.Hour(),
		EndMinute:   e.Minute(),
		Timezone:    "UTC",
	}
}

// skipNearMidnight saute le test si l'heure courante UTC est telle qu'une fenêtre
// relative de ±3h enjamberait minuit (les fenêtres ne supportant pas le wrap,
// cf. IsValid). Zone à risque ultra-étroite (≈3h/24) ; un skip y est préférable à
// un flake. En CI déterministe (heure contrôlée) ce skip ne se déclenche jamais.
func skipNearMidnight(t *testing.T) {
	h := time.Now().UTC().Hour()
	if h >= 21 || h < 3 {
		t.Skip("fenêtre relative enjamberait minuit UTC (non supporté par design), skip")
	}
}

func TestVeridianSendingWindowGate_NoConfigIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	for i := 0; i < 5; i++ {
		delay, closed := env.worker.veridianSendingWindowGate(workspace, nil, entry)
		assert.False(t, closed)
		assert.Zero(t, delay)
	}
}

func TestVeridianSendingWindowGate_WithinWindowAllows(t *testing.T) {
	skipNearMidnight(t)
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	// Fenêtre ouverte maintenant via workspace settings.
	workspace.Settings.VeridianSendingWindow = windowAround(-1*time.Hour, 1*time.Hour)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, closed := env.worker.veridianSendingWindowGate(workspace, nil, entry)
	assert.False(t, closed)
	assert.Zero(t, delay)
}

func TestVeridianSendingWindowGate_OutsideWindowReschedules(t *testing.T) {
	skipNearMidnight(t)
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	// Fenêtre fermée maintenant (ouvre dans ~2h).
	workspace.Settings.VeridianSendingWindow = windowAround(2*time.Hour, 3*time.Hour)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, closed := env.worker.veridianSendingWindowGate(workspace, nil, entry)
	assert.True(t, closed)
	assert.Greater(t, delay, time.Second)
	assert.LessOrEqual(t, delay, veridianSendingWindowMaxRetryDelay)
}

func TestVeridianSendingWindowGate_PayloadTakesPrecedence(t *testing.T) {
	skipNearMidnight(t)
	env := newVeridianThrottleTestEnv(t)
	// Workspace ouvert maintenant, mais le payload pose une fenêtre FERMÉE → le
	// payload prime (niveau le plus spécifique).
	workspace := veridianTestWorkspace(nil, 6000)
	workspace.Settings.VeridianSendingWindow = windowAround(-1*time.Hour, 1*time.Hour)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{
		VeridianSendingWindow: windowAround(2*time.Hour, 3*time.Hour),
	})

	delay, closed := env.worker.veridianSendingWindowGate(workspace, nil, entry)
	assert.True(t, closed)
	assert.Greater(t, delay, time.Duration(0))
}

func TestVeridianSendingWindowGate_InfraTakesPrecedenceOverWorkspace(t *testing.T) {
	skipNearMidnight(t)
	env := newVeridianThrottleTestEnv(t)
	// Workspace fermé, infra ouverte → infra prime sur workspace.
	workspace := veridianTestWorkspace(nil, 6000)
	workspace.Settings.VeridianSendingWindow = windowAround(2*time.Hour, 3*time.Hour)
	provider := &domain.EmailProvider{
		Kind:                  domain.EmailProviderKindSMTP,
		RateLimitPerMinute:    6000,
		VeridianSendingWindow: windowAround(-1*time.Hour, 1*time.Hour),
	}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, closed := env.worker.veridianSendingWindowGate(workspace, provider, entry)
	assert.False(t, closed)
	assert.Zero(t, delay)
}

func TestVeridianSendingWindowGate_InvalidWindowIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	// Fenêtre invalide (plage vide) = jamais bloquer (non-régression).
	workspace.Settings.VeridianSendingWindow = &domain.VeridianSendingWindow{StartHour: 9, EndHour: 9}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	delay, closed := env.worker.veridianSendingWindowGate(workspace, nil, entry)
	assert.False(t, closed)
	assert.Zero(t, delay)
}

func TestVeridianResolveSendingWindow_Cascade(t *testing.T) {
	skipNearMidnight(t) // les fenêtres relatives ±3h enjamberaient minuit (non supporté), cf. autres tests du fichier
	openW := windowAround(-1*time.Hour, 1*time.Hour)
	closedW := windowAround(2*time.Hour, 3*time.Hour)

	t.Run("payload prime", func(t *testing.T) {
		ws := veridianTestWorkspace(nil, 6000)
		ws.Settings.VeridianSendingWindow = closedW
		provider := &domain.EmailProvider{VeridianSendingWindow: closedW}
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{VeridianSendingWindow: openW})
		got := veridianResolveSendingWindow(ws, provider, entry)
		require.NotNil(t, got)
		assert.Equal(t, openW, got)
	})

	t.Run("infra avant workspace", func(t *testing.T) {
		ws := veridianTestWorkspace(nil, 6000)
		ws.Settings.VeridianSendingWindow = closedW
		provider := &domain.EmailProvider{VeridianSendingWindow: openW}
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
		got := veridianResolveSendingWindow(ws, provider, entry)
		assert.Equal(t, openW, got)
	})

	t.Run("workspace en dernier", func(t *testing.T) {
		ws := veridianTestWorkspace(nil, 6000)
		ws.Settings.VeridianSendingWindow = openW
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
		got := veridianResolveSendingWindow(ws, nil, entry)
		assert.Equal(t, openW, got)
	})

	t.Run("rien -> nil", func(t *testing.T) {
		ws := veridianTestWorkspace(nil, 6000)
		entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})
		assert.Nil(t, veridianResolveSendingWindow(ws, nil, entry))
	})
}
