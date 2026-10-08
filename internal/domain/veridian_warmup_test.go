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
			got := VeridianWarmupCapForDay(tt.started, tt.schedule, tt.stepDays, tt.now, nil)
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
	day0a := VeridianWarmupCapForDay(ptrTimeWarmup(base), schedule, 1, base, nil)
	day0b := VeridianWarmupCapForDay(ptrTimeWarmup(base), schedule, 1, base.Add(3*time.Hour), nil)
	assert.Equal(t, 5, day0a, "J1 = palier 0 = plafond TOTAL de l'infra (toutes classes)")
	assert.Equal(t, day0a, day0b, "le cap est un total unique, déterministe, indépendant du destinataire")

	// Et il monte par paliers sur le TOTAL (pas par classe) : J2 = 10 total, etc.
	day1 := VeridianWarmupCapForDay(ptrTimeWarmup(base), schedule, 1, base.Add(24*time.Hour), nil)
	assert.Equal(t, 10, day1, "J2 = palier 1 = nouveau plafond TOTAL")
}

func TestVeridianWarmupStep(t *testing.T) {
	schedule := []int{1, 2, 5, 10}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	cur, total := VeridianWarmupStep(nil, schedule, 1, base, nil)
	assert.Equal(t, 0, cur)
	assert.Equal(t, 0, total)

	cur, total = VeridianWarmupStep(ptrTimeWarmup(base), schedule, 1, base, nil)
	assert.Equal(t, 1, cur, "day 0 = step 1/4 (1-indexed)")
	assert.Equal(t, 4, total)

	cur, total = VeridianWarmupStep(ptrTimeWarmup(base), schedule, 1, base.Add(48*time.Hour), nil)
	assert.Equal(t, 3, cur, "day 2 = step 3/4")
	assert.Equal(t, 4, total)

	cur, total = VeridianWarmupStep(ptrTimeWarmup(base), schedule, 1, base.Add(100*24*time.Hour), nil)
	assert.Equal(t, 4, cur, "clamp to last step")
	assert.Equal(t, 4, total)
}

// Lot 5 (08/10/2026) : le palier avance par JOUR DE COMPTE (date civile du fuseau du
// profil), pas par tranche de 24 h écoulées. Echec démontré contre l'ancien calcul
// (elapsedDays = heures / 24) : démarré à 18h Paris, le palier 1 arrivait le lendemain
// à 18h, alors que les compteurs journaliers repartent à minuit Paris.
func TestVeridianWarmupCapForDay_AdvancesByAccountDay(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	assert.NoError(t, err)
	schedule := []int{1, 2, 5, 10}
	// Démarré le 10/06/2026 à 18h00 Paris (16h00 UTC).
	started := time.Date(2026, 6, 10, 18, 0, 0, 0, paris)

	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{"meme soir, jour 0", time.Date(2026, 6, 10, 23, 59, 0, 0, paris), 1},
		{"minuit Paris, jour 1 (seulement 6 h apres le debut)", time.Date(2026, 6, 11, 0, 0, 0, 0, paris), 2},
		{"lendemain 17h59, deja jour 1 (l'ancien calcul disait encore jour 0)", time.Date(2026, 6, 11, 17, 59, 0, 0, paris), 2},
		{"surlendemain 00h01, jour 2", time.Date(2026, 6, 12, 0, 1, 0, 0, paris), 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianWarmupCapForDay(&started, schedule, 1, tt.now, paris))
		})
	}

	// Sans fuseau (nil = UTC) : 23h30 UTC et 00h30 UTC du lendemain sont deux jours de compte.
	utcStart := time.Date(2026, 6, 10, 23, 30, 0, 0, time.UTC)
	assert.Equal(t, 2, VeridianWarmupCapForDay(&utcStart, schedule, 1, time.Date(2026, 6, 11, 0, 30, 0, 0, time.UTC), nil))

	// Changement d'heure : le 29/03/2026 dure 23 h a Paris ; le jour 1 d'un warmup
	// demarre le 28 a 12h00 commence quand meme a minuit local, pas a 12h00 + 24 h.
	dstStart := time.Date(2026, 3, 28, 12, 0, 0, 0, paris)
	assert.Equal(t, 2, VeridianWarmupCapForDay(&dstStart, schedule, 1, time.Date(2026, 3, 29, 0, 0, 0, 0, paris), paris))
	assert.Equal(t, 5, VeridianWarmupCapForDay(&dstStart, schedule, 1, time.Date(2026, 3, 30, 0, 0, 0, 0, paris), paris))

	// Le palier affiche (Step) suit la meme regle.
	cur, total := VeridianWarmupStep(&started, schedule, 1, time.Date(2026, 6, 11, 0, 0, 0, 0, paris), paris)
	assert.Equal(t, 2, cur)
	assert.Equal(t, 4, total)
}
