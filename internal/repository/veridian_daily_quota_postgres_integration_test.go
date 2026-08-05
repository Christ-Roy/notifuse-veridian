package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// This test uses PostgreSQL row locks and unique constraints for real. Run with
// VERIDIAN_TEST_POSTGRES_DSN pointing at a disposable database.
func TestVeridianDailyQuotaRepository_PostgresConcurrency(t *testing.T) {
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set VERIDIAN_TEST_POSTGRES_DSN to a disposable PostgreSQL database")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.Ping())
	ctx := context.Background()
	_, err = db.Exec(`
		DROP TABLE IF EXISTS veridian_daily_quota_reservations, veridian_daily_quota_counters, message_history;
		CREATE TABLE message_history (
			id VARCHAR(255) PRIMARY KEY, contact_email VARCHAR(255) NOT NULL,
			sent_at TIMESTAMPTZ NOT NULL, failed_at TIMESTAMPTZ,
			veridian_sender_email VARCHAR(255), veridian_provider_class VARCHAR(64), veridian_profile_id VARCHAR(255)
		);
		CREATE TABLE veridian_daily_quota_counters (
			workspace_id VARCHAR(255) NOT NULL, quota_day DATE NOT NULL, quota_kind VARCHAR(32) NOT NULL,
			sender_domain VARCHAR(255) NOT NULL, provider_class VARCHAR(64) NOT NULL DEFAULT '',
			used INTEGER NOT NULL DEFAULT 0 CHECK (used >= 0), created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(workspace_id, quota_day, quota_kind, sender_domain, provider_class)
		);
		CREATE TABLE veridian_daily_quota_reservations (
			workspace_id VARCHAR(255) NOT NULL, message_id VARCHAR(255) NOT NULL, quota_kind VARCHAR(32) NOT NULL,
			quota_day DATE NOT NULL, sender_domain VARCHAR(255) NOT NULL, provider_class VARCHAR(64) NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(workspace_id, message_id, quota_kind)
		)
	`)
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws").Return(db, nil).AnyTimes()
	repo := NewMessageHistoryRepository(workspaceRepo)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	reserve := func(messageID string, cap int) domain.VeridianDailyQuotaReservationResult {
		result, err := repo.ReserveDailyQuota(ctx, "ws", domain.VeridianDailyQuotaReservation{
			MessageID: messageID, Cap: cap,
			Key: domain.VeridianDailyQuotaKey{Day: day, Kind: domain.VeridianDailyQuotaKindProviderClass, SenderDomain: "send.test", ProviderClass: "microsoft"},
		})
		require.NoError(t, err)
		return result
	}
	reserveProfile := func(messageID, profileID string, cap int) domain.VeridianDailyQuotaReservationResult {
		result, err := repo.ReserveDailyQuota(ctx, "ws", domain.VeridianDailyQuotaReservation{
			MessageID: messageID, Cap: cap,
			Key: domain.VeridianDailyQuotaKey{Day: day, Kind: domain.VeridianDailyQuotaKindProfile, ProfileID: profileID},
		})
		require.NoError(t, err)
		return result
	}

	t.Run("distinct messages never exceed cap", func(t *testing.T) {
		var accepted atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if reserve(fmt.Sprintf("msg-%02d", i), 7).Reserved {
					accepted.Add(1)
				}
			}(i)
		}
		wg.Wait()
		require.Equal(t, int32(7), accepted.Load())
		var used int
		require.NoError(t, db.QueryRow(`SELECT used FROM veridian_daily_quota_counters`).Scan(&used))
		require.Equal(t, 7, used)
	})

	t.Run("same message is idempotent under concurrency", func(t *testing.T) {
		_, err := db.Exec(`TRUNCATE veridian_daily_quota_reservations, veridian_daily_quota_counters`)
		require.NoError(t, err)
		var created atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if result := reserve("same-message", 7); result.Reserved && !result.AlreadyReserved {
					created.Add(1)
				}
			}()
		}
		wg.Wait()
		require.Equal(t, int32(1), created.Load())
		var used int
		require.NoError(t, db.QueryRow(`SELECT used FROM veridian_daily_quota_counters`).Scan(&used))
		require.Equal(t, 1, used)
	})

	t.Run("same message rotates reservation at UTC day boundary", func(t *testing.T) {
		_, err := db.Exec(`TRUNCATE veridian_daily_quota_reservations, veridian_daily_quota_counters, message_history`)
		require.NoError(t, err)
		reserveOnDay := func(quotaDay time.Time) domain.VeridianDailyQuotaReservationResult {
			result, err := repo.ReserveDailyQuota(ctx, "ws", domain.VeridianDailyQuotaReservation{
				MessageID: "overnight-message", Cap: 7,
				Key: domain.VeridianDailyQuotaKey{Day: quotaDay, Kind: domain.VeridianDailyQuotaKindProviderClass, SenderDomain: "send.test", ProviderClass: "microsoft"},
			})
			require.NoError(t, err)
			return result
		}
		require.True(t, reserveOnDay(day.Add(-24*time.Hour)).Reserved)
		today := reserveOnDay(day)
		require.True(t, today.Reserved)
		require.False(t, today.AlreadyReserved)
		var reservations, counters int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_daily_quota_reservations`).Scan(&reservations))
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_daily_quota_counters`).Scan(&counters))
		require.Equal(t, 1, reservations)
		require.Equal(t, 2, counters)
	})

	t.Run("profile cap is exact isolated concurrent and idempotent", func(t *testing.T) {
		_, err := db.Exec(`TRUNCATE veridian_daily_quota_reservations, veridian_daily_quota_counters, message_history`)
		require.NoError(t, err)
		var acceptedA, acceptedB atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 40; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				if reserveProfile(fmt.Sprintf("a-%02d", i), "gmail-a", 7).Reserved {
					acceptedA.Add(1)
				}
			}(i)
			go func(i int) {
				defer wg.Done()
				if reserveProfile(fmt.Sprintf("b-%02d", i), "gmail-b", 7).Reserved {
					acceptedB.Add(1)
				}
			}(i)
		}
		wg.Wait()
		require.Equal(t, int32(7), acceptedA.Load())
		require.Equal(t, int32(7), acceptedB.Load())
		first := reserveProfile("same-profile-message", "gmail-c", 7)
		second := reserveProfile("same-profile-message", "gmail-c", 7)
		require.True(t, first.Reserved)
		require.False(t, first.AlreadyReserved)
		require.True(t, second.Reserved)
		require.True(t, second.AlreadyReserved)
	})

	t.Run("historical seed ignores failed rows", func(t *testing.T) {
		_, err := db.Exec(`TRUNCATE veridian_daily_quota_reservations, veridian_daily_quota_counters, message_history`)
		require.NoError(t, err)
		for i := 0; i < 5; i++ {
			var failed interface{}
			if i == 4 {
				failed = time.Now()
			}
			_, err = db.Exec(`INSERT INTO message_history(id, contact_email, sent_at, failed_at, veridian_sender_email, veridian_provider_class) VALUES($1,$2,$3,$4,$5,$6)`, fmt.Sprintf("history-%d", i), "lead@example.com", day.Add(time.Hour), failed, "bot@send.test", "microsoft")
			require.NoError(t, err)
		}
		result := reserve("new-message", 5)
		require.True(t, result.Reserved)
		require.Equal(t, 5, result.Used)
		require.False(t, reserve("over-cap", 5).Reserved)
	})
}
