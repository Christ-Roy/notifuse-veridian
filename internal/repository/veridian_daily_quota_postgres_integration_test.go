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
			veridian_sender_email VARCHAR(255), veridian_provider_class VARCHAR(64), veridian_profile_id VARCHAR(255),
			transactional_notification_id VARCHAR(32), veridian_message_type VARCHAR(16)
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

	// Lot 4 (08/10/2026) : le jour de compte suit le fuseau de la fenetre d'envoi du
	// profil. Ici Europe/Paris, avec un vrai PostgreSQL : bornes du jour, graine du
	// compteur, transactionnel ecarte, bascule de minuit, jours de changement d'heure.
	t.Run("paris day: seed window, transactional excluded, midnight flip, no double count", func(t *testing.T) {
		_, err := db.Exec(`TRUNCATE veridian_daily_quota_reservations, veridian_daily_quota_counters, message_history`)
		require.NoError(t, err)
		paris, err := time.LoadLocation("Europe/Paris")
		require.NoError(t, err)
		insert := func(id string, at time.Time, messageType string) {
			_, err := db.Exec(`INSERT INTO message_history(id, contact_email, sent_at, veridian_profile_id, veridian_message_type) VALUES($1,'lead@example.com',$2,'p1',NULLIF($3,''))`, id, at, messageType)
			require.NoError(t, err)
		}
		// 8 octobre 2026 a Paris = du 7 a 22h00 UTC (inclus) au 8 a 22h00 UTC (exclu).
		insert("before-midnight", time.Date(2026, 10, 7, 21, 59, 59, 0, time.UTC), "") // 7 octobre 23:59:59 Paris
		insert("first-second", time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC), "")      // 8 octobre 00:00:00 Paris
		insert("noon", time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), "")
		insert("noon-transactional", time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC), "transactional")
		insert("last-second", time.Date(2026, 10, 8, 21, 59, 59, 0, time.UTC), "")
		insert("next-day", time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC), "") // 9 octobre 00:00:00 Paris

		parisDay := func(at time.Time) domain.VeridianDay { return domain.VeridianDayAt(at, paris) }
		reserveDay := func(messageID string, d domain.VeridianDay, cap int) domain.VeridianDailyQuotaReservationResult {
			result, err := repo.ReserveDailyQuota(ctx, "ws", domain.VeridianDailyQuotaReservation{
				MessageID: messageID, Cap: cap,
				Key: domain.VeridianDailyQuotaKey{Day: d.Label, DayStart: d.Start, DayEnd: d.End, Kind: domain.VeridianDailyQuotaKindProfile, ProfileID: "p1"},
			})
			require.NoError(t, err)
			return result
		}
		oct8 := parisDay(time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC))
		// Graine = 3 (first-second, noon, last-second) : ni la veille, ni le lendemain, ni le transactionnel.
		first := reserveDay("m-1", oct8, 4)
		require.True(t, first.Reserved)
		require.Equal(t, 4, first.Used, "3 envois du jour de Paris + 1 reservation")
		require.False(t, reserveDay("m-2", oct8, 4).Reserved, "plafond exact : 4 maximum")
		// Retour du meme message : idempotent, aucun double compte.
		again := reserveDay("m-1", oct8, 4)
		require.True(t, again.AlreadyReserved)
		var used int
		require.NoError(t, db.QueryRow(`SELECT used FROM veridian_daily_quota_counters WHERE quota_day='2026-10-08'`).Scan(&used))
		require.Equal(t, 4, used)

		// Bascule de minuit a Paris : le meme message retente le 9 change de compteur
		// (reservation du 8 remplacee), le compteur du 8 reste intact, celui du 9 est seme
		// avec le seul envoi "next-day".
		oct9 := parisDay(time.Date(2026, 10, 8, 22, 30, 0, 0, time.UTC))
		require.Equal(t, "2026-10-09", oct9.LabelDate())
		rotated := reserveDay("m-1", oct9, 4)
		require.True(t, rotated.Reserved)
		require.False(t, rotated.AlreadyReserved)
		require.Equal(t, 2, rotated.Used, "1 envoi du 9 + la reservation")
		require.NoError(t, db.QueryRow(`SELECT used FROM veridian_daily_quota_counters WHERE quota_day='2026-10-08'`).Scan(&used))
		require.Equal(t, 4, used, "le compteur de la veille n'est pas touche")
		var reservations int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_daily_quota_reservations WHERE message_id='m-1'`).Scan(&reservations))
		require.Equal(t, 1, reservations)
	})

	t.Run("paris daylight saving days last 25 and 23 hours", func(t *testing.T) {
		_, err := db.Exec(`TRUNCATE veridian_daily_quota_reservations, veridian_daily_quota_counters, message_history`)
		require.NoError(t, err)
		paris, err := time.LoadLocation("Europe/Paris")
		require.NoError(t, err)
		insert := func(id string, at time.Time) {
			_, err := db.Exec(`INSERT INTO message_history(id, contact_email, sent_at, veridian_profile_id) VALUES($1,'lead@example.com',$2,'p1')`, id, at)
			require.NoError(t, err)
		}
		reserveDay := func(messageID string, d domain.VeridianDay, cap int) domain.VeridianDailyQuotaReservationResult {
			result, err := repo.ReserveDailyQuota(ctx, "ws", domain.VeridianDailyQuotaReservation{
				MessageID: messageID, Cap: cap,
				Key: domain.VeridianDailyQuotaKey{Day: d.Label, DayStart: d.Start, DayEnd: d.End, Kind: domain.VeridianDailyQuotaKindProfile, ProfileID: "p1"},
			})
			require.NoError(t, err)
			return result
		}
		// 25 octobre 2026 (retour a l'heure d'hiver) : de 22h00 UTC le 24 a 23h00 UTC le 25.
		insert("a-0000", time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC))    // 00:00 Paris (UTC+2)
		insert("a-2230z", time.Date(2026, 10, 25, 22, 59, 59, 0, time.UTC)) // 23:59:59 Paris (UTC+1) : dans le jour de 25 h
		insert("a-next", time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC))    // 26 octobre 00:00 Paris
		autumn := domain.VeridianDayAt(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), paris)
		require.Equal(t, 25*time.Hour, autumn.End.Sub(autumn.Start))
		r := reserveDay("autumn", autumn, 10)
		require.True(t, r.Reserved)
		require.Equal(t, 3, r.Used, "2 envois dans les 25 h + la reservation, rien du 26")

		// 29 mars 2026 (passage a l'heure d'ete) : de 23h00 UTC le 28 a 22h00 UTC le 29 (23 h).
		insert("s-0000", time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC))
		insert("s-last", time.Date(2026, 3, 29, 21, 59, 59, 0, time.UTC))
		insert("s-next", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC))
		spring := domain.VeridianDayAt(time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC), paris)
		require.Equal(t, 23*time.Hour, spring.End.Sub(spring.Start))
		r = reserveDay("spring", spring, 10)
		require.True(t, r.Reserved)
		require.Equal(t, 3, r.Used)
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
