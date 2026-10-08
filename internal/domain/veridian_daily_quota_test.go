package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianDailyQuotaContracts(t *testing.T) {
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	reservation := VeridianDailyQuotaReservation{
		MessageID: "msg-1",
		Cap:       5,
		Key: VeridianDailyQuotaKey{
			WorkspaceID:   "ws-1",
			Day:           day,
			Kind:          VeridianDailyQuotaKindProviderClass,
			SenderDomain:  "send.test",
			ProviderClass: "microsoft",
		},
	}
	require.Equal(t, "provider_class", VeridianDailyQuotaKindProviderClass)
	require.Equal(t, "warmup", VeridianDailyQuotaKindWarmup)
	require.Equal(t, "profile", VeridianDailyQuotaKindProfile)
	assert.Equal(t, day, reservation.Key.Day)
	assert.Equal(t, 5, reservation.Cap)
}

func TestVeridianDailyQuotaProfileKeyUsesExactIntegrationID(t *testing.T) {
	key := VeridianDailyQuotaKey{Kind: VeridianDailyQuotaKindProfile, ProfileID: "integration-uuid-1"}
	assert.Equal(t, "integration-uuid-1", key.ProfileID)
	assert.Empty(t, key.SenderDomain)
}

// Lot 4 (08/10/2026) : la cle du compteur porte le libelle du jour local et ses bornes exactes.
func TestVeridianDailyQuotaKeyCarriesTheLocalDayBounds(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)
	day := VeridianDayAt(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), paris)
	key := VeridianDailyQuotaKey{Day: day.Label, DayStart: day.Start, DayEnd: day.End}
	assert.Equal(t, "2026-10-25", key.Day.Format("2006-01-02"))
	assert.Equal(t, 25*time.Hour, key.DayEnd.Sub(key.DayStart), "retour a l'heure d'hiver : 25 h")
}
