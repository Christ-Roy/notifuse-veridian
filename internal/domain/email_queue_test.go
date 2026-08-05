package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type setNextRetryContractRepo struct {
	EmailQueueRepository
	called       bool
	refundCalled bool
}

func (r *setNextRetryContractRepo) SetNextRetry(context.Context, string, string, time.Time) error {
	r.called = true
	return nil
}

func (r *setNextRetryContractRepo) SetNextRetryAndRefundAttempt(context.Context, string, string, time.Time) error {
	r.refundCalled = true
	return nil
}

func TestEmailQueueRepository_SetNextRetryContract(t *testing.T) {
	var repo EmailQueueRepository = &setNextRetryContractRepo{}
	require.NoError(t, repo.SetNextRetry(context.Background(), "ws", "entry", time.Now()))
	require.NoError(t, repo.SetNextRetryAndRefundAttempt(context.Background(), "ws", "entry", time.Now()))
	assert.True(t, repo.(*setNextRetryContractRepo).called)
	assert.True(t, repo.(*setNextRetryContractRepo).refundCalled)
	// Compile-time guard for the transaction-bearing method retained by the
	// embedded upstream contract.
	var _ interface {
		EnqueueTx(context.Context, *sql.Tx, string, []*EmailQueueEntry) error
	} = repo
}

// Veridian — les champs throttle par classe du payload doivent survivre au
// round-trip JSONB (stockage queue) et rester absents du JSON quand non
// configurés (compat ascendante : les entrées enqueueées avant le deploy
// n'ont pas ces clés et doivent se désérialiser à l'identique).
func TestEmailQueuePayload_VeridianProviderThrottleRoundTrip(t *testing.T) {
	t.Run("fields survive JSON round-trip", func(t *testing.T) {
		payload := EmailQueuePayload{
			Subject:                    "s",
			RateLimitPerMinute:         100,
			VeridianProviderClass:      ProviderClassGoogle,
			VeridianProviderClassRates: map[string]float64{"google": 0.5, "corporate": 30},
		}

		raw, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, ProviderClassGoogle, decoded.VeridianProviderClass)
		assert.Equal(t, payload.VeridianProviderClassRates, decoded.VeridianProviderClassRates)
	})

	t.Run("omitted when unset (upstream payloads unchanged)", func(t *testing.T) {
		raw, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "veridian_provider_class")

		// Entrée pré-deploy (sans les clés veridian) : zéro valeurs, pas d'erreur
		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal([]byte(`{"subject":"old","rate_limit_per_minute":25}`), &decoded))
		assert.Empty(t, decoded.VeridianProviderClass)
		assert.Nil(t, decoded.VeridianProviderClassRates)
		assert.Nil(t, decoded.VeridianProviderClassDailyCap)
		assert.Zero(t, decoded.VeridianPerRecipientDailyCap)
	})

	t.Run("daily caps survive JSON round-trip", func(t *testing.T) {
		payload := EmailQueuePayload{
			Subject:                       "s",
			RateLimitPerMinute:            100,
			VeridianProviderClassDailyCap: map[string]int{"google": 1, "microsoft": 50},
			VeridianPerRecipientDailyCap:  1,
		}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, map[string]int{"google": 1, "microsoft": 50}, decoded.VeridianProviderClassDailyCap)
		assert.Equal(t, 1, decoded.VeridianPerRecipientDailyCap)
	})

	t.Run("daily caps omitted when unset", func(t *testing.T) {
		raw, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "veridian_provider_class_daily_cap")
		assert.NotContains(t, string(raw), "veridian_per_recipient_daily_cap")
	})

	t.Run("per-sender daily cap survives JSON round-trip (warmup IP, V53)", func(t *testing.T) {
		payload := EmailQueuePayload{
			Subject:                   "s",
			RateLimitPerMinute:        100,
			VeridianPerSenderDailyCap: 20,
		}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"veridian_per_sender_daily_cap":20`)

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, 20, decoded.VeridianPerSenderDailyCap)
	})

	t.Run("per-sender daily cap omitted when unset", func(t *testing.T) {
		raw, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "veridian_per_sender_daily_cap")
	})

	t.Run("sending window survives JSON round-trip", func(t *testing.T) {
		payload := EmailQueuePayload{
			Subject:            "s",
			RateLimitPerMinute: 100,
			VeridianSendingWindow: &VeridianSendingWindow{
				Days: []int{1, 2, 3, 4, 5}, StartHour: 9, EndHour: 18, Timezone: "Europe/Paris",
			},
		}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.NotNil(t, decoded.VeridianSendingWindow)
		assert.Equal(t, []int{1, 2, 3, 4, 5}, decoded.VeridianSendingWindow.Days)
		assert.Equal(t, 9, decoded.VeridianSendingWindow.StartHour)
		assert.Equal(t, "Europe/Paris", decoded.VeridianSendingWindow.Timezone)
	})

	t.Run("sending window omitted when unset", func(t *testing.T) {
		raw, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "veridian_sending_window")
	})

	// Le PIÈGE POINTEUR du jitter : *0 (jitter explicitement désactivé) DOIT
	// survivre au round-trip JSONB comme distinct de nil (non configuré). Si
	// omitempty droppait *0, le worker confondrait "désactivé" avec "défaut cold".
	t.Run("jitter pct *0 survives round-trip as present (not dropped)", func(t *testing.T) {
		zero := 0.0
		payload := EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100, VeridianJitterPct: &zero}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		// *0 doit être SÉRIALISÉ (un pointeur non-nil n'est pas omis par omitempty).
		assert.Contains(t, string(raw), "veridian_jitter_pct")

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.NotNil(t, decoded.VeridianJitterPct, "*0 doit rester non-nil après round-trip")
		assert.Equal(t, 0.0, *decoded.VeridianJitterPct)
	})

	t.Run("jitter pct value survives round-trip", func(t *testing.T) {
		v := 0.3
		payload := EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100, VeridianJitterPct: &v}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.NotNil(t, decoded.VeridianJitterPct)
		assert.Equal(t, 0.3, *decoded.VeridianJitterPct)
	})

	t.Run("jitter pct omitted (nil) when unset", func(t *testing.T) {
		raw, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "veridian_jitter_pct")

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal([]byte(`{"subject":"old"}`), &decoded))
		assert.Nil(t, decoded.VeridianJitterPct, "absent → nil (non configuré, le gate applique le défaut cold)")
	})

	t.Run("content hash survives round-trip and omitted when empty", func(t *testing.T) {
		payload := EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100, VeridianContentHash: "deadbeefdeadbeefdeadbeefdeadbeef"}
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "veridian_content_hash")

		var decoded EmailQueuePayload
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, "deadbeefdeadbeefdeadbeefdeadbeef", decoded.VeridianContentHash)

		// Vide → absent (omitempty), non-régression.
		rawEmpty, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
		require.NoError(t, err)
		assert.NotContains(t, string(rawEmpty), "veridian_content_hash")
	})
}

func TestEmailQueueStatus_Values(t *testing.T) {
	tests := []struct {
		name     string
		status   EmailQueueStatus
		expected string
	}{
		{
			name:     "pending status",
			status:   EmailQueueStatusPending,
			expected: "pending",
		},
		{
			name:     "processing status",
			status:   EmailQueueStatusProcessing,
			expected: "processing",
		},
		{
			name:     "failed status",
			status:   EmailQueueStatusFailed,
			expected: "failed",
		},
		// Note: There is no "sent" status - entries are deleted immediately after successful send
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestEmailQueueSourceType_Values(t *testing.T) {
	tests := []struct {
		name       string
		sourceType EmailQueueSourceType
		expected   string
	}{
		{
			name:       "broadcast source",
			sourceType: EmailQueueSourceBroadcast,
			expected:   "broadcast",
		},
		{
			name:       "automation source",
			sourceType: EmailQueueSourceAutomation,
			expected:   "automation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.sourceType))
		})
	}
}

func TestEmailQueuePriorityMarketing(t *testing.T) {
	assert.Equal(t, 5, EmailQueuePriorityMarketing)
}

func TestCalculateNextRetryTime(t *testing.T) {
	// Ensure env var is not set for this test (use default 1 minute base)
	os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")

	tests := []struct {
		name            string
		attempts        int
		expectedMinutes int
	}{
		{
			name:            "zero attempts defaults to 1 minute",
			attempts:        0,
			expectedMinutes: 1,
		},
		{
			name:            "negative attempts defaults to 1 minute",
			attempts:        -1,
			expectedMinutes: 1,
		},
		{
			name:            "first attempt - 1 minute backoff",
			attempts:        1,
			expectedMinutes: 1,
		},
		{
			name:            "second attempt - 2 minutes backoff",
			attempts:        2,
			expectedMinutes: 2,
		},
		{
			name:            "third attempt - 4 minutes backoff",
			attempts:        3,
			expectedMinutes: 4,
		},
		{
			name:            "fourth attempt - 8 minutes backoff",
			attempts:        4,
			expectedMinutes: 8,
		},
		{
			name:            "fifth attempt - 16 minutes backoff",
			attempts:        5,
			expectedMinutes: 16,
		},
		{
			name:            "tenth attempt - 512 minutes backoff",
			attempts:        10,
			expectedMinutes: 512,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now().UTC()
			result := CalculateNextRetryTime(tt.attempts)
			after := time.Now().UTC()

			expectedDuration := time.Duration(tt.expectedMinutes) * time.Minute

			// Result should be between before+expectedDuration and after+expectedDuration
			// Allow a small margin for test execution time
			minExpected := before.Add(expectedDuration)
			maxExpected := after.Add(expectedDuration).Add(time.Second) // 1 second margin

			assert.True(t, result.After(minExpected) || result.Equal(minExpected),
				"result %v should be >= %v", result, minExpected)
			assert.True(t, result.Before(maxExpected) || result.Equal(maxExpected),
				"result %v should be <= %v", result, maxExpected)
		})
	}
}

func TestGetEmailQueueRetryBase(t *testing.T) {
	t.Run("default value when not set", func(t *testing.T) {
		os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")
		assert.Equal(t, 1*time.Minute, getEmailQueueRetryBase())
	})

	t.Run("custom value from environment", func(t *testing.T) {
		os.Setenv("EMAIL_QUEUE_RETRY_BASE", "2s")
		defer os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")
		assert.Equal(t, 2*time.Second, getEmailQueueRetryBase())
	})

	t.Run("custom value with different duration", func(t *testing.T) {
		os.Setenv("EMAIL_QUEUE_RETRY_BASE", "30s")
		defer os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")
		assert.Equal(t, 30*time.Second, getEmailQueueRetryBase())
	})

	t.Run("invalid value uses default", func(t *testing.T) {
		os.Setenv("EMAIL_QUEUE_RETRY_BASE", "invalid")
		defer os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")
		assert.Equal(t, 1*time.Minute, getEmailQueueRetryBase())
	})

	t.Run("empty value uses default", func(t *testing.T) {
		os.Setenv("EMAIL_QUEUE_RETRY_BASE", "")
		defer os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")
		assert.Equal(t, 1*time.Minute, getEmailQueueRetryBase())
	})
}

func TestCalculateNextRetryTime_WithCustomBase(t *testing.T) {
	// Test with custom base of 2 seconds
	os.Setenv("EMAIL_QUEUE_RETRY_BASE", "2s")
	defer os.Unsetenv("EMAIL_QUEUE_RETRY_BASE")

	tests := []struct {
		name             string
		attempts         int
		expectedDuration time.Duration
	}{
		{
			name:             "first attempt - 2 seconds backoff",
			attempts:         1,
			expectedDuration: 2 * time.Second,
		},
		{
			name:             "second attempt - 4 seconds backoff",
			attempts:         2,
			expectedDuration: 4 * time.Second,
		},
		{
			name:             "third attempt - 8 seconds backoff",
			attempts:         3,
			expectedDuration: 8 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now().UTC()
			result := CalculateNextRetryTime(tt.attempts)
			after := time.Now().UTC()

			// Result should be between before+expectedDuration and after+expectedDuration
			// Allow a small margin for test execution time
			minExpected := before.Add(tt.expectedDuration)
			maxExpected := after.Add(tt.expectedDuration).Add(time.Second) // 1 second margin

			assert.True(t, result.After(minExpected) || result.Equal(minExpected),
				"result %v should be >= %v", result, minExpected)
			assert.True(t, result.Before(maxExpected) || result.Equal(maxExpected),
				"result %v should be <= %v", result, maxExpected)
		})
	}
}

func TestEmailQueuePayload_ToSendEmailProviderRequest(t *testing.T) {
	t.Run("converts all fields correctly", func(t *testing.T) {
		payload := EmailQueuePayload{
			FromAddress:        "sender@example.com",
			FromName:           "Test Sender",
			Subject:            "Test Subject",
			HTMLContent:        "<html><body>Test</body></html>",
			RateLimitPerMinute: 100,
			EmailOptions: EmailOptions{
				ListUnsubscribeURL: "https://example.com/unsubscribe",
			},
		}

		provider := &EmailProvider{
			Kind: EmailProviderKindSMTP,
			SMTP: &SMTPSettings{
				Host: "smtp.example.com",
				Port: 587,
			},
		}

		result := payload.ToSendEmailProviderRequest(
			"workspace123",
			"integration456",
			"message789",
			"recipient@example.com",
			provider,
		)

		require.NotNil(t, result)
		assert.Equal(t, "workspace123", result.WorkspaceID)
		assert.Equal(t, "integration456", result.IntegrationID)
		assert.Equal(t, "message789", result.MessageID)
		assert.Equal(t, "sender@example.com", result.FromAddress)
		assert.Equal(t, "Test Sender", result.FromName)
		assert.Equal(t, "recipient@example.com", result.To)
		assert.Equal(t, "Test Subject", result.Subject)
		assert.Equal(t, "<html><body>Test</body></html>", result.Content)
		assert.Equal(t, provider, result.Provider)
		assert.Equal(t, "https://example.com/unsubscribe", result.EmailOptions.ListUnsubscribeURL)
	})

	t.Run("handles nil provider", func(t *testing.T) {
		payload := EmailQueuePayload{
			FromAddress: "sender@example.com",
			FromName:    "Test Sender",
			Subject:     "Test Subject",
			HTMLContent: "<html><body>Test</body></html>",
		}

		result := payload.ToSendEmailProviderRequest(
			"workspace123",
			"integration456",
			"message789",
			"recipient@example.com",
			nil,
		)

		require.NotNil(t, result)
		assert.Nil(t, result.Provider)
		assert.Equal(t, "workspace123", result.WorkspaceID)
		assert.Equal(t, "recipient@example.com", result.To)
	})

	t.Run("handles empty payload fields", func(t *testing.T) {
		payload := EmailQueuePayload{}

		provider := &EmailProvider{
			Kind: EmailProviderKindSES,
		}

		result := payload.ToSendEmailProviderRequest(
			"workspace123",
			"integration456",
			"message789",
			"recipient@example.com",
			provider,
		)

		require.NotNil(t, result)
		assert.Equal(t, "", result.FromAddress)
		assert.Equal(t, "", result.FromName)
		assert.Equal(t, "", result.Subject)
		assert.Equal(t, "", result.Content)
	})
}

func TestEmailQueueEntry_DefaultValues(t *testing.T) {
	entry := EmailQueueEntry{}

	// Verify zero values for optional fields
	assert.Equal(t, "", entry.ID)
	assert.Equal(t, EmailQueueStatus(""), entry.Status)
	assert.Equal(t, 0, entry.Priority)
	assert.Equal(t, 0, entry.Attempts)
	assert.Equal(t, 0, entry.MaxAttempts)
	assert.Nil(t, entry.LastError)
	assert.Nil(t, entry.NextRetryAt)
	assert.Nil(t, entry.ProcessedAt)
}

func TestEmailQueueStats_DefaultValues(t *testing.T) {
	stats := EmailQueueStats{}

	assert.Equal(t, int64(0), stats.Pending)
	assert.Equal(t, int64(0), stats.Processing)
	assert.Equal(t, int64(0), stats.Failed)
	// Note: Sent entries are deleted immediately, not tracked in stats
}

// TestEmailQueuePayload_VeridianExcludedProviderClassesRoundTrip couvre le champ
// EmailQueuePayload.VeridianExcludedProviderClasses (copié à l'enqueue) : il
// survit au round-trip JSON et reste absent (omitempty) quand vide.
func TestEmailQueuePayload_VeridianExcludedProviderClassesRoundTrip(t *testing.T) {
	payload := EmailQueuePayload{
		Subject:                         "s",
		RateLimitPerMinute:              100,
		VeridianExcludedProviderClasses: []string{"microsoft"},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "veridian_excluded_provider_classes")

	var decoded EmailQueuePayload
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, []string{"microsoft"}, decoded.VeridianExcludedProviderClasses)

	// Vide → absent (omitempty), non-régression.
	rawEmpty, err := json.Marshal(EmailQueuePayload{Subject: "s", RateLimitPerMinute: 100})
	require.NoError(t, err)
	assert.NotContains(t, string(rawEmpty), "veridian_excluded_provider_classes")
}
