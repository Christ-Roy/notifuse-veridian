package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// businessHours = lundi-vendredi 9h-18h Europe/Paris, la config cold canonique.
func businessHours() *VeridianSendingWindow {
	return &VeridianSendingWindow{
		Days:      []int{1, 2, 3, 4, 5},
		StartHour: 9,
		EndHour:   18,
		Timezone:  "Europe/Paris",
	}
}

func TestVeridianSendingWindow_IsZero(t *testing.T) {
	var nilW *VeridianSendingWindow
	assert.True(t, nilW.IsZero())
	assert.True(t, (&VeridianSendingWindow{}).IsZero())
	assert.False(t, businessHours().IsZero())
}

func TestVeridianSendingWindow_IsValid(t *testing.T) {
	tests := []struct {
		name string
		w    *VeridianSendingWindow
		want bool
	}{
		{"nil", nil, false},
		{"empty plage vide", &VeridianSendingWindow{}, false},
		{"business hours", businessHours(), true},
		{"start==end (plage vide)", &VeridianSendingWindow{StartHour: 9, EndHour: 9}, false},
		{"end < start (inversée)", &VeridianSendingWindow{StartHour: 18, EndHour: 9}, false},
		{"end=24 minuit ok", &VeridianSendingWindow{StartHour: 9, EndHour: 24}, true},
		{"start hour hors borne", &VeridianSendingWindow{StartHour: 25, EndHour: 26}, false},
		{"minutes seules", &VeridianSendingWindow{StartHour: 9, StartMinute: 30, EndHour: 9, EndMinute: 45}, true},
		{"end>24 invalide", &VeridianSendingWindow{StartHour: 9, EndHour: 25}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.w.IsValid())
		})
	}
}

func TestVeridianSendingWindow_IsWithinWindow(t *testing.T) {
	w := businessHours()
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)

	// Mercredi 2026-06-17 (jour 3) à diverses heures Paris.
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"mercredi 10h dans fenêtre", time.Date(2026, 6, 17, 10, 0, 0, 0, paris), true},
		{"mercredi 9h pile (inclus)", time.Date(2026, 6, 17, 9, 0, 0, 0, paris), true},
		{"mercredi 18h pile (exclu)", time.Date(2026, 6, 17, 18, 0, 0, 0, paris), false},
		{"mercredi 17h59 (inclus)", time.Date(2026, 6, 17, 17, 59, 0, 0, paris), true},
		{"mercredi 8h59 (avant)", time.Date(2026, 6, 17, 8, 59, 0, 0, paris), false},
		{"mercredi 3h du matin", time.Date(2026, 6, 17, 3, 0, 0, 0, paris), false},
		// 2026-06-20 = samedi (jour 6), hors Days.
		{"samedi 10h hors jour", time.Date(2026, 6, 20, 10, 0, 0, 0, paris), false},
		// 2026-06-21 = dimanche (jour 0), hors Days.
		{"dimanche 10h hors jour", time.Date(2026, 6, 21, 10, 0, 0, 0, paris), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, w.IsWithinWindow(c.t, ""))
		})
	}
}

func TestVeridianSendingWindow_IsWithinWindow_InvalidIsAlways24x7(t *testing.T) {
	// Fenêtre invalide = pas de fenêtre = tout passe (non-régression).
	w := &VeridianSendingWindow{StartHour: 9, EndHour: 9} // plage vide
	assert.True(t, w.IsWithinWindow(time.Date(2026, 6, 21, 3, 0, 0, 0, time.UTC), ""))

	var nilW *VeridianSendingWindow
	assert.True(t, nilW.IsWithinWindow(time.Now(), ""))
}

func TestVeridianSendingWindow_TimezoneFallback(t *testing.T) {
	// Fenêtre sans Timezone → fallback fourni par l'appelant.
	w := &VeridianSendingWindow{Days: []int{1, 2, 3, 4, 5}, StartHour: 9, EndHour: 18}
	paris, _ := time.LoadLocation("Europe/Paris")

	// 2026-06-17 12h UTC = 14h Paris (dans 9-18) → dans fenêtre via fallback Paris.
	noonUTC := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	assert.True(t, w.IsWithinWindow(noonUTC, "Europe/Paris"))

	// 2026-06-17 7h UTC = 9h Paris → dans fenêtre Paris mais HORS en UTC (7h).
	sevenUTC := time.Date(2026, 6, 17, 7, 0, 0, 0, time.UTC)
	assert.True(t, w.IsWithinWindow(sevenUTC, "Europe/Paris"))
	assert.False(t, w.IsWithinWindow(sevenUTC, "")) // fallback UTC → 7h hors 9-18

	// Timezone invalide → retombe sur UTC, pas de panic.
	assert.True(t, w.IsWithinWindow(noonUTC, "Not/AZone"))
	_ = paris
}

func TestVeridianSendingWindow_NextOpening(t *testing.T) {
	w := businessHours()
	paris, _ := time.LoadLocation("Europe/Paris")

	t.Run("dans fenêtre -> now", func(t *testing.T) {
		now := time.Date(2026, 6, 17, 10, 0, 0, 0, paris)
		assert.Equal(t, now, w.NextOpening(now, ""))
	})

	t.Run("avant ouverture meme jour -> 9h aujourd'hui", func(t *testing.T) {
		now := time.Date(2026, 6, 17, 6, 0, 0, 0, paris) // mercredi 6h
		next := w.NextOpening(now, "")
		nextParis := next.In(paris)
		assert.Equal(t, 9, nextParis.Hour())
		assert.Equal(t, 17, nextParis.Day())
		assert.True(t, next.After(now) || next.Equal(now))
	})

	t.Run("apres fermeture -> 9h jour ouvrable suivant", func(t *testing.T) {
		now := time.Date(2026, 6, 17, 20, 0, 0, 0, paris) // mercredi 20h
		next := w.NextOpening(now, "").In(paris)
		assert.Equal(t, 9, next.Hour())
		assert.Equal(t, 18, next.Day()) // jeudi
	})

	t.Run("vendredi soir -> lundi 9h (saute le week-end)", func(t *testing.T) {
		now := time.Date(2026, 6, 19, 20, 0, 0, 0, paris) // vendredi 20h
		next := w.NextOpening(now, "").In(paris)
		assert.Equal(t, time.Monday, next.Weekday())
		assert.Equal(t, 9, next.Hour())
		assert.Equal(t, 22, next.Day()) // lundi 22 juin
	})

	t.Run("samedi -> lundi 9h", func(t *testing.T) {
		now := time.Date(2026, 6, 20, 12, 0, 0, 0, paris) // samedi
		next := w.NextOpening(now, "").In(paris)
		assert.Equal(t, time.Monday, next.Weekday())
		assert.Equal(t, 9, next.Hour())
	})

	t.Run("fenêtre invalide -> now", func(t *testing.T) {
		bad := &VeridianSendingWindow{StartHour: 9, EndHour: 9}
		now := time.Now()
		assert.Equal(t, now, bad.NextOpening(now, ""))
	})
}

func TestVeridianSendingWindowFromMetadata(t *testing.T) {
	t.Run("nil metadata", func(t *testing.T) {
		assert.Nil(t, VeridianSendingWindowFromMetadata(nil))
	})

	t.Run("clé absente", func(t *testing.T) {
		assert.Nil(t, VeridianSendingWindowFromMetadata(MapOfAny{"autre": 1}))
	})

	t.Run("parse complet", func(t *testing.T) {
		md := MapOfAny{
			VeridianSendingWindowMetadataKey: map[string]any{
				"days":       []any{1.0, 2.0, 3.0, 4.0, 5.0},
				"start_hour": 9.0,
				"end_hour":   18.0,
				"timezone":   "Europe/Paris",
			},
		}
		w := VeridianSendingWindowFromMetadata(md)
		require.NotNil(t, w)
		assert.Equal(t, []int{1, 2, 3, 4, 5}, w.Days)
		assert.Equal(t, 9, w.StartHour)
		assert.Equal(t, 18, w.EndHour)
		assert.Equal(t, "Europe/Paris", w.Timezone)
	})

	t.Run("minutes + days hors borne filtrés", func(t *testing.T) {
		md := MapOfAny{
			VeridianSendingWindowMetadataKey: map[string]any{
				"days":         []any{1.0, 9.0, -1.0, 5.0}, // 9 et -1 filtrés
				"start_hour":   8.0,
				"start_minute": 30.0,
				"end_hour":     17.0,
				"end_minute":   45.0,
			},
		}
		w := VeridianSendingWindowFromMetadata(md)
		require.NotNil(t, w)
		assert.Equal(t, []int{1, 5}, w.Days)
		assert.Equal(t, 30, w.StartMinute)
		assert.Equal(t, 45, w.EndMinute)
	})

	t.Run("config invalide -> nil", func(t *testing.T) {
		md := MapOfAny{
			VeridianSendingWindowMetadataKey: map[string]any{
				"start_hour": 18.0,
				"end_hour":   9.0, // inversée
			},
		}
		assert.Nil(t, VeridianSendingWindowFromMetadata(md))
	})

	t.Run("valeur non-map -> nil", func(t *testing.T) {
		md := MapOfAny{VeridianSendingWindowMetadataKey: "pas une map"}
		assert.Nil(t, VeridianSendingWindowFromMetadata(md))
	})
}

func TestVeridianParseWeekdayList(t *testing.T) {
	assert.Nil(t, veridianParseWeekdayList(""))
	assert.Nil(t, veridianParseWeekdayList("   "))
	assert.Equal(t, []int{1, 2, 3, 4, 5}, veridianParseWeekdayList("1,2,3,4,5"))
	assert.Equal(t, []int{1, 5}, veridianParseWeekdayList(" 1 , 5 "))
	// Valeurs hors borne et non-numériques ignorées.
	assert.Equal(t, []int{0, 6}, veridianParseWeekdayList("0,7,abc,6,-1"))
}
