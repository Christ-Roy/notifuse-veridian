package queue

import (
	"regexp"
	"strings"
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

// --- Fiche 62 : variante structurée veridianSendingWindowVerdict + description de fenêtre ---

func TestVeridianDescribeWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		window   *domain.VeridianSendingWindow
		fallback string
		want     string
	}{
		{"jours ouvrés, fuseau propre", &domain.VeridianSendingWindow{Days: []int{1, 2, 3, 4, 5}, StartHour: 8, EndHour: 19, Timezone: "Europe/Paris"}, "UTC", "lun,mar,mer,jeu,ven 08:00-19:00 Europe/Paris"},
		{"tous les jours, minutes, fuseau du workspace", &domain.VeridianSendingWindow{StartHour: 8, StartMinute: 30, EndHour: 19, EndMinute: 5}, "Europe/Paris", "tous les jours 08:30-19:05 Europe/Paris"},
		{"aucun fuseau nulle part = UTC", &domain.VeridianSendingWindow{Days: []int{0, 6}, StartHour: 9, EndHour: 12}, "", "dim,sam 09:00-12:00 UTC"},
		{"jour hors plage ignoré", &domain.VeridianSendingWindow{Days: []int{1, 9, -1}, StartHour: 9, EndHour: 12, Timezone: "UTC"}, "", "lun 09:00-12:00 UTC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, veridianDescribeWindow(tc.window, tc.fallback))
		})
	}
}

func TestVeridianSendingWindowVerdict_OpenWindowPassesWithLocalTimeAndLimit(t *testing.T) {
	skipNearMidnight(t)
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.VeridianSendingWindow = windowAround(-1*time.Hour, 1*time.Hour)
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	v := env.worker.veridianSendingWindowVerdict(ws, nil, entry)
	assert.False(t, v.Blocked())
	assert.Equal(t, domain.VeridianVerdictPass, v.Verdict)
	assert.Equal(t, domain.VeridianGateWindow, v.Gate)
	assert.Regexp(t, regexp.MustCompile(`^(dim|lun|mar|mer|jeu|ven|sam) \d\d:\d\d$`), v.Value)
	assert.Equal(t, veridianDescribeWindow(ws.Settings.VeridianSendingWindow, ""), v.Limit)
	assert.Zero(t, v.Delay)
	assert.Empty(t, v.Reason)
}

func TestVeridianSendingWindowVerdict_NoWindowConfiguredIsNoop(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	v := env.worker.veridianSendingWindowVerdict(veridianTestWorkspace(nil, 6000), nil,
		veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{}))
	assert.False(t, v.Blocked())
	assert.Nil(t, v.Value)
	assert.Nil(t, v.Limit)
	assert.Equal(t, "no window configured", v.Detail)
}

func TestVeridianSendingWindowVerdict_UsesWorkspaceTimezoneWhenWindowHasNone(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.Timezone = "Asia/Tokyo"
	win := closedWindow()
	win.Timezone = ""
	ws.Settings.VeridianSendingWindow = win

	v := env.worker.veridianSendingWindowVerdict(ws, nil, veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{}))
	require.True(t, v.Blocked())
	assert.True(t, strings.HasSuffix(v.Limit.(string), "Asia/Tokyo"), "fuseau de repli du workspace : %v", v.Limit)
}

// Fenêtre fermée jusqu'à un jour précis (ici : 2 jours plus tard, donc plus de 24 h).
// Prouve : le délai est borné à 24 h comme avant, mais la RÉOUVERTURE réelle est tracée
// (« reopens= ») et tombe le bon jour à l'heure d'ouverture. Le cas du samedi 10/10
// (réouverture lundi) est la même mécanique : voir le test sur Saturday ci-dessous.
func TestVeridianSendingWindowVerdict_ClosedUntilLaterDay_DelayBoundedButReopeningIsExact(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	openDay := (int(time.Now().UTC().Weekday()) + 2) % 7
	ws.Settings.VeridianSendingWindow = &domain.VeridianSendingWindow{
		Days: []int{openDay}, StartHour: 8, EndHour: 19, Timezone: "UTC",
	}
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	before := time.Now()
	v := env.worker.veridianSendingWindowVerdict(ws, nil, entry)
	require.True(t, v.Blocked())
	assert.Equal(t, domain.VeridianReasonWindowClosed, v.Reason)
	assert.Equal(t, veridianSendingWindowMaxRetryDelay, v.Delay, "réouverture dans plus de 24 h : le délai est borné")

	require.True(t, strings.HasPrefix(v.Detail, "reopens="), v.Detail)
	opening, err := time.Parse(time.RFC3339, strings.TrimPrefix(v.Detail, "reopens="))
	require.NoError(t, err)
	assert.Equal(t, time.Weekday(openDay), opening.UTC().Weekday())
	assert.Equal(t, 8, opening.UTC().Hour())
	assert.Equal(t, 0, opening.UTC().Minute())
	assert.True(t, opening.After(before.Add(veridianSendingWindowMaxRetryDelay)), "la vraie réouverture est au-delà du délai borné")

	// Valeur = jour et heure locaux de l'évaluation, limite = la fenêtre lisible.
	assert.Regexp(t, regexp.MustCompile(`^(dim|lun|mar|mer|jeu|ven|sam) \d\d:\d\d$`), v.Value)
	assert.Equal(t, veridianDescribeWindow(ws.Settings.VeridianSendingWindow, ""), v.Limit)
}

// Cas mesuré le samedi 10/10/2026 : fenêtre lun-ven 08:00-19:00, samedi midi = fermée,
// réouverture lundi 08:00. La porte s'appuie sur window.IsWithinWindow / NextOpening :
// on les éprouve sur ce samedi précis (le gate lit l'horloge réelle, d'où le test du
// calcul qu'il consomme et, plus haut, du gate lui-même avec une réouverture lointaine).
func TestVeridianSendingWindow_SaturdayClosed_ReopensMonday0800(t *testing.T) {
	win := &domain.VeridianSendingWindow{Days: []int{1, 2, 3, 4, 5}, StartHour: 8, EndHour: 19, Timezone: "UTC"}
	saturdayNoon := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	require.Equal(t, time.Saturday, saturdayNoon.Weekday())

	assert.False(t, win.IsWithinWindow(saturdayNoon, ""), "samedi : fenêtre fermée")
	assert.Equal(t, time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC), win.NextOpening(saturdayNoon, "").UTC(), "réouverture lundi 08:00")
	assert.True(t, win.IsWithinWindow(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC), ""), "lundi 09:00 : ouverte")
	assert.Equal(t, "lun,mar,mer,jeu,ven 08:00-19:00 UTC", veridianDescribeWindow(win, ""))
}

func TestVeridianSendingWindowGate_WrapperMatchesVerdict(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := veridianTestWorkspace(nil, 6000)
	ws.Settings.VeridianSendingWindow = closedWindow()
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	v := env.worker.veridianSendingWindowVerdict(ws, nil, entry)
	delay, closed := env.worker.veridianSendingWindowGate(ws, nil, entry)
	assert.True(t, closed)
	assert.Equal(t, v.Blocked(), closed)
	assert.InDelta(t, v.Delay.Seconds(), delay.Seconds(), 2)
}
