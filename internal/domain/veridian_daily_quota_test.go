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
	assert.Equal(t, day, reservation.Key.Day)
	assert.Equal(t, 5, reservation.Cap)
}
