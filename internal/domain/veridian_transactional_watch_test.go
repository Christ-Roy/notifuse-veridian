package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianEvaluateTransactionalWatch(t *testing.T) {
	cases := []struct {
		name  string
		in    VeridianTransactionalWatchInput
		level string
		codes []string
	}{
		{"calme", VeridianTransactionalWatchInput{SentToday: 12, SentPrevious7Days: 70, Sent7d: 90}, VeridianWatchLevelOK, nil},
		{"profil neuf sous le plancher", VeridianTransactionalWatchInput{SentToday: 80}, VeridianWatchLevelOK, nil},
		{"volume : a surveiller", VeridianTransactionalWatchInput{SentToday: 120, Sent7d: 120}, VeridianWatchLevelWatch, []string{VeridianWatchCodeVolume}},
		{"volume : boucle cote client", VeridianTransactionalWatchInput{SentToday: 900, SentPrevious7Days: 70, Sent7d: 970}, VeridianWatchLevelAlert, []string{VeridianWatchCodeVolume}},
		{"gros volume mais habituel", VeridianTransactionalWatchInput{SentToday: 1200, SentPrevious7Days: 7000, Sent7d: 8200}, VeridianWatchLevelOK, nil},
		{"rejets durs a surveiller", VeridianTransactionalWatchInput{Sent7d: 100, HardBounces7d: 3}, VeridianWatchLevelWatch, []string{VeridianWatchCodeHardBounce}},
		{"rejets durs en alerte", VeridianTransactionalWatchInput{Sent7d: 100, HardBounces7d: 6}, VeridianWatchLevelAlert, []string{VeridianWatchCodeHardBounce}},
		{"plaintes en alerte et refus de politique a surveiller", VeridianTransactionalWatchInput{Sent7d: 1000, Complaints7d: 4, PolicyRefusals7d: 60}, VeridianWatchLevelAlert, []string{VeridianWatchCodeComplaint, VeridianWatchCodePolicyRefusal}},
		{"echantillon trop petit : pas de taux", VeridianTransactionalWatchInput{Sent7d: 10, HardBounces7d: 5, Complaints7d: 2}, VeridianWatchLevelOK, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := VeridianEvaluateTransactionalWatch("tx", "Transactionnel", c.in)
			assert.Equal(t, c.level, w.Level)
			assert.False(t, w.Blocking, "l'alerte ne bloque jamais")
			got := []string{}
			for _, a := range w.Alerts {
				got = append(got, a.Code)
			}
			assert.ElementsMatch(t, append([]string{}, c.codes...), got)
		})
	}

	w := VeridianUnknownTransactionalWatch("tx", "T", "boom")
	assert.Equal(t, VeridianWatchLevelUnknown, w.Level)
	assert.NotEqual(t, VeridianWatchLevelOK, w.Level)
	body, err := json.Marshal(w)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"alerts":[]`)
}
