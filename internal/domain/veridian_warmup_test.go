package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func ptrTimeWarmup(t time.Time) *time.Time { return &t }

func TestVeridianWarmupActive(t *testing.T) {
	now := time.Now().UTC()
	assert.False(t, VeridianWarmupActive(nil, []int{1, 2}), "nil start = inactive")
	assert.False(t, VeridianWarmupActive(ptrTimeWarmup(time.Time{}), []int{1, 2}), "zero start = inactive")
	assert.False(t, VeridianWarmupActive(ptrTimeWarmup(now), nil), "nil schedule = inactive")
	assert.False(t, VeridianWarmupActive(ptrTimeWarmup(now), []int{}), "empty schedule = inactive")
	assert.True(t, VeridianWarmupActive(ptrTimeWarmup(now), []int{1}), "start + schedule = active")
}

func TestVeridianWarmupCapForDay(t *testing.T) {
	schedule := []int{1, 2, 5, 10, 25, 50, 100}
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		started  *time.Time
		schedule []int
		stepDays int
		now      time.Time
		want     int
	}{
		// Non-régression : pas de warmup configuré → 0 (= « pas de cap warmup », pas « cap 0 »).
		{"no start", nil, schedule, 1, base, 0},
		{"empty schedule", ptrTimeWarmup(base), nil, 1, base, 0},

		// stepDays=1 (un palier par jour).
		{"day 0 = palier 0", ptrTimeWarmup(base), schedule, 1, base, 1},
		{"day 0 even hours later", ptrTimeWarmup(base), schedule, 1, base.Add(6 * time.Hour), 1},
		{"day 1 = palier 1", ptrTimeWarmup(base), schedule, 1, base.Add(24 * time.Hour), 2},
		{"day 2 = palier 2", ptrTimeWarmup(base), schedule, 1, base.Add(48 * time.Hour), 5},
		{"day 6 = palier 6 (dernier)", ptrTimeWarmup(base), schedule, 1, base.Add(6 * 24 * time.Hour), 100},
		{"day 30 = clamp dernier palier", ptrTimeWarmup(base), schedule, 1, base.Add(30 * 24 * time.Hour), 100},

		// stepDays=2 (deux jours par palier).
		{"step2 day 0 = palier 0", ptrTimeWarmup(base), schedule, 2, base, 1},
		{"step2 day 1 = palier 0", ptrTimeWarmup(base), schedule, 2, base.Add(24 * time.Hour), 1},
		{"step2 day 2 = palier 1", ptrTimeWarmup(base), schedule, 2, base.Add(48 * time.Hour), 2},
		{"step2 day 3 = palier 1", ptrTimeWarmup(base), schedule, 2, base.Add(72 * time.Hour), 2},
		{"step2 day 4 = palier 2", ptrTimeWarmup(base), schedule, 2, base.Add(96 * time.Hour), 5},

		// stepDays<=0 → défaut 1.
		{"stepDays 0 defaults to 1", ptrTimeWarmup(base), schedule, 0, base.Add(24 * time.Hour), 2},
		{"stepDays negative defaults to 1", ptrTimeWarmup(base), schedule, -5, base.Add(48 * time.Hour), 5},

		// startedAt dans le futur → reste au palier le plus conservateur.
		{"future start = palier 0", ptrTimeWarmup(base.Add(48 * time.Hour)), schedule, 1, base, 1},

		// Valeur de courbe négative = config malformée → traitée comme « pas de cap warmup ».
		{"negative schedule value", ptrTimeWarmup(base), []int{-1, 5}, 1, base, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VeridianWarmupCapForDay(tt.started, tt.schedule, tt.stepDays, tt.now)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestVeridianWarmupCapForDay_IsTotalNotPerClass verrouille la sémantique CORRIGÉE
// (2026-06-19) : le cap warmup est un plafond HOLISTIQUE du VOLUME TOTAL de l'infra
// par jour, TOUTES classes destinataires confondues — PAS un cap par classe. Garde-fou
// contractuel : la fonction ne prend AUCUN paramètre de classe destinataire (sa
// signature est (startedAt, schedule, stepDays, now)), donc le palier renvoyé est par
// construction un nombre UNIQUE pour l'infra, indépendant du destinataire. C'est ce qui
// rend le warmup robuste aux classes MX dans le gate (veridian_daily_cap.go branche
// warmup compte le TOTAL par domaine émetteur via CountSentSinceForSenderDomain).
func TestVeridianWarmupCapForDay_IsTotalNotPerClass(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	schedule := []int{5, 10, 25}

	// Le cap du jour est un SEUL nombre (le palier), pas une map par classe : appelé
	// deux fois avec les MÊMES paramètres temporels, il renvoie la MÊME valeur — il ne
	// dépend d'aucune classe destinataire (la fonction n'en reçoit pas). « J1 = 5 max,
	// point » : ce 5 est le total de l'infra, pas 5 par classe.
	day0a := VeridianWarmupCapForDay(ptrTimeWarmup(base), schedule, 1, base)
	day0b := VeridianWarmupCapForDay(ptrTimeWarmup(base), schedule, 1, base.Add(3*time.Hour))
	assert.Equal(t, 5, day0a, "J1 = palier 0 = plafond TOTAL de l'infra (toutes classes)")
	assert.Equal(t, day0a, day0b, "le cap est un total unique, déterministe, indépendant du destinataire")

	// Et il monte par paliers sur le TOTAL (pas par classe) : J2 = 10 total, etc.
	day1 := VeridianWarmupCapForDay(ptrTimeWarmup(base), schedule, 1, base.Add(24*time.Hour))
	assert.Equal(t, 10, day1, "J2 = palier 1 = nouveau plafond TOTAL")
}

func TestVeridianWarmupStep(t *testing.T) {
	schedule := []int{1, 2, 5, 10}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	cur, total := VeridianWarmupStep(nil, schedule, 1, base)
	assert.Equal(t, 0, cur)
	assert.Equal(t, 0, total)

	cur, total = VeridianWarmupStep(ptrTimeWarmup(base), schedule, 1, base)
	assert.Equal(t, 1, cur, "day 0 = step 1/4 (1-indexed)")
	assert.Equal(t, 4, total)

	cur, total = VeridianWarmupStep(ptrTimeWarmup(base), schedule, 1, base.Add(48*time.Hour))
	assert.Equal(t, 3, cur, "day 2 = step 3/4")
	assert.Equal(t, 4, total)

	cur, total = VeridianWarmupStep(ptrTimeWarmup(base), schedule, 1, base.Add(100*24*time.Hour))
	assert.Equal(t, 4, cur, "clamp to last step")
	assert.Equal(t, 4, total)
}
