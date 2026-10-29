package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests de caractérisation de la rampe (warmup) et de la fenêtre d'envoi (lot L4, 29/10/2026),
// écrits depuis les mutants qui survivaient (bornes de la courbe, minutes, jours, 7e jour).
// La rampe borne le volume envoyé par un domaine neuf, la fenêtre l'heure d'envoi : une borne
// décalée d'un cran se paie en réputation d'envoi, pas en test rouge.

func TestVeridianCharac_Warmup_BorneExacteDeLaCourbe(t *testing.T) {
	schedule := []int{1, 2, 5}
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	at := func(days int) time.Time { return start.AddDate(0, 0, days) }

	// stepDays=1 : le jour len(schedule) tombe exactement sur la fin de la courbe (plein régime, pas de panique).
	for _, c := range []struct{ day, want int }{{0, 1}, {1, 2}, {2, 5}, {3, 5}, {4, 5}, {30, 5}} {
		assert.Equal(t, c.want, VeridianWarmupCapForDay(&start, schedule, 1, at(c.day), nil), "jour %d", c.day)
	}
	// stepDays=2 : idx = jour/2 ; jour 6 donne idx == len(schedule) pile.
	for _, c := range []struct{ day, want int }{{0, 1}, {1, 1}, {2, 2}, {3, 2}, {4, 5}, {5, 5}, {6, 5}, {7, 5}} {
		assert.Equal(t, c.want, VeridianWarmupCapForDay(&start, schedule, 2, at(c.day), nil), "pas 2, jour %d", c.day)
	}
}

func TestVeridianCharac_Warmup_EtapeAffichee(t *testing.T) {
	schedule := []int{1, 2, 5}
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ day, step, wantCur int }{{0, 1, 1}, {2, 1, 3}, {3, 1, 3}, {50, 1, 3}, {4, 2, 3}, {6, 2, 3}} {
		cur, total := VeridianWarmupStep(&start, schedule, c.step, start.AddDate(0, 0, c.day), nil)
		assert.Equal(t, c.wantCur, cur, "jour %d pas %d", c.day, c.step)
		assert.Equal(t, 3, total)
	}
	cur, total := VeridianWarmupStep(nil, schedule, 1, start, nil)
	assert.Equal(t, 0, cur)
	assert.Equal(t, 0, total)
}

func TestVeridianCharac_Warmup_ValeurNegativeEtDebutDansLeFutur(t *testing.T) {
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, 0, VeridianWarmupCapForDay(&start, []int{-5, 10}, 1, start, nil), "palier négatif = pas de cap, jamais un blocage")
	before := start.AddDate(0, 0, -3)
	assert.Equal(t, 10, VeridianWarmupCapForDay(&start, []int{10, 20}, 1, before, nil), "début dans le futur : premier palier, le plus prudent")
}

func TestVeridianCharac_FenetreEnvoi_IsZeroChampParChamp(t *testing.T) {
	var nilw *VeridianSendingWindow
	assert.True(t, nilw.IsZero())
	assert.True(t, (&VeridianSendingWindow{}).IsZero())
	for name, w := range map[string]VeridianSendingWindow{
		"jours":    {Days: []int{1}},
		"début h":  {StartHour: 9},
		"début mn": {StartMinute: 5},
		"fin h":    {EndHour: 18},
		"fin mn":   {EndMinute: 5},
		"fuseau":   {Timezone: "Europe/Paris"},
	} {
		w := w
		assert.False(t, w.IsZero(), "un seul champ renseigné suffit pour que la fenêtre existe : %s", name)
	}
}

func TestVeridianCharac_FenetreEnvoi_ValiditeBornesEtMinutes(t *testing.T) {
	cases := []struct {
		name string
		w    VeridianSendingWindow
		ok   bool
	}{
		{"9h-18h", VeridianSendingWindow{StartHour: 9, EndHour: 18}, true},
		{"9h00-9h30 : les minutes comptent", VeridianSendingWindow{StartHour: 9, EndHour: 9, EndMinute: 30}, true},
		{"9h30-9h00 : inversée", VeridianSendingWindow{StartHour: 9, StartMinute: 30, EndHour: 9}, false},
		{"9h30-9h30 : vide", VeridianSendingWindow{StartHour: 9, StartMinute: 30, EndHour: 9, EndMinute: 30}, false},
		{"0h00-0h30 : une demi-heure après minuit", VeridianSendingWindow{StartHour: 0, EndHour: 0, EndMinute: 30}, true},
		{"jusqu'à 24h00", VeridianSendingWindow{StartHour: 8, EndHour: 24}, true},
		{"début heure négative", VeridianSendingWindow{StartHour: -1, EndHour: 18}, false},
		{"début heure 24", VeridianSendingWindow{StartHour: 24, EndHour: 24, EndMinute: 30}, false},
		{"début minute négative", VeridianSendingWindow{StartHour: 9, StartMinute: -1, EndHour: 18}, false},
		{"début minute 60", VeridianSendingWindow{StartHour: 9, StartMinute: 60, EndHour: 18}, false},
		{"fin heure négative", VeridianSendingWindow{StartHour: 9, EndHour: -1}, false},
		{"fin heure 25", VeridianSendingWindow{StartHour: 9, EndHour: 25}, false},
		{"fin minute négative", VeridianSendingWindow{StartHour: 9, EndHour: 18, EndMinute: -1}, false},
		{"fin minute 60", VeridianSendingWindow{StartHour: 9, EndHour: 18, EndMinute: 60}, false},
	}
	for _, c := range cases {
		w := c.w
		assert.Equal(t, c.ok, w.IsValid(), c.name)
	}
	var nilw *VeridianSendingWindow
	assert.False(t, nilw.IsValid())
}

func TestVeridianCharac_FenetreEnvoi_AuMinutePres(t *testing.T) {
	w := &VeridianSendingWindow{StartHour: 9, StartMinute: 30, EndHour: 17, EndMinute: 45, Timezone: "UTC"}
	day := func(h, m int) time.Time { return time.Date(2026, 6, 3, h, m, 0, 0, time.UTC) } // mercredi
	assert.False(t, w.IsWithinWindow(day(9, 29), ""))
	assert.True(t, w.IsWithinWindow(day(9, 30), ""), "ouverture incluse")
	assert.True(t, w.IsWithinWindow(day(17, 44), ""))
	assert.False(t, w.IsWithinWindow(day(17, 45), ""), "fermeture exclusive")
}

func TestVeridianCharac_FenetreEnvoi_ProchaineOuvertureAu7eJour(t *testing.T) {
	// Seul le mercredi est autorisé et on est mercredi, fenêtre fermée : la prochaine ouverture est
	// mercredi prochain, soit exactement 7 jours plus tard (et non « maintenant »).
	w := &VeridianSendingWindow{Days: []int{int(time.Wednesday)}, StartHour: 9, EndHour: 18, Timezone: "UTC"}
	now := time.Date(2026, 6, 3, 20, 0, 0, 0, time.UTC)
	require.Equal(t, time.Wednesday, now.Weekday())
	got := w.NextOpening(now, "")
	assert.Equal(t, time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC), got)

	// Avant l'ouverture du jour autorisé : ouverture du jour même.
	early := time.Date(2026, 6, 3, 7, 0, 0, 0, time.UTC)
	assert.Equal(t, time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC), w.NextOpening(early, ""))
	// Dans la fenêtre : maintenant. Fenêtre invalide : maintenant.
	in := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, in, w.NextOpening(in, ""))
	assert.Equal(t, in, (&VeridianSendingWindow{}).NextOpening(in, ""))
}

func TestVeridianCharac_FenetreEnvoi_JoursDesMetadonnees(t *testing.T) {
	md := MapOfAny{VeridianSendingWindowMetadataKey: map[string]any{
		"start_hour": 9, "end_hour": 18,
		"days": []any{0, 3, 6, 7, -1, "x"},
	}}
	w := VeridianSendingWindowFromMetadata(md)
	require.NotNil(t, w)
	assert.Equal(t, []int{0, 3, 6}, w.Days, "dimanche (0) et samedi (6) sont des jours valides, 7 et -1 sont ignorés")
	assert.Nil(t, VeridianSendingWindowFromMetadata(MapOfAny{VeridianSendingWindowMetadataKey: map[string]any{"start_hour": 18, "end_hour": 9}}))
	assert.Nil(t, VeridianSendingWindowFromMetadata(nil))
}
