package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lot 4 (08/10/2026) : les compteurs commerciaux ecartent les messages
// transactionnels, et le fusible de reputation se compte par profil.
// Chaque test attend la condition EXACTE dans la requete : sans elle (ancien
// comportement) la requete ne correspond pas et le test echoue.

const commercialPredicate = `AND veridian_message_type IS DISTINCT FROM 'transactional' AND transactional_notification_id IS NULL`

func quoted(s string) string { return regexp.QuoteMeta(s) }

func TestMessageHistoryRepository_CommercialCountersExcludeTransactional(t *testing.T) {
	since := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	const ws = "workspace-123"

	cases := []struct {
		name   string
		expect string // fragment de requete attendu (avant le predicat commercial)
		args   []driver.Value
		call   func(r domain.MessageHistoryRepository) error
	}{
		{"destinataire", `FROM message_history WHERE contact_email = $1 AND sent_at >= $2 AND failed_at IS NULL `, []driver.Value{"a@b.fr", since},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForContact(context.Background(), ws, "a@b.fr", since)
				return err
			}},
		{"adresse emettrice", `WHERE veridian_sender_email = lower($1) AND sent_at >= $2 AND failed_at IS NULL `, []driver.Value{"bot@send.fr", since},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForSender(context.Background(), ws, "bot@send.fr", since)
				return err
			}},
		{"domaine emetteur", `lower(split_part(veridian_sender_email, '@', 2)) = lower($2) `, []driver.Value{since, "send.fr"},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForSenderDomain(context.Background(), ws, "send.fr", since)
				return err
			}},
		{"rejets durs", `bounce_type = 'PolicyBounce') AND lower(split_part(veridian_sender_email, '@', 2)) = lower($2) `, []driver.Value{since, "send.fr"},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountHardBouncedSinceForSenderDomain(context.Background(), ws, "send.fr", since)
				return err
			}},
		{"plaintes", `complained_at IS NOT NULL AND lower(split_part(veridian_sender_email, '@', 2)) = lower($2) `, []driver.Value{since, "send.fr"},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountComplainedSinceForSenderDomain(context.Background(), ws, "send.fr", since)
				return err
			}},
		{"classe destinataire", `WHERE veridian_provider_class = $1 AND sent_at >= $2 AND failed_at IS NULL `, []driver.Value{"google", since},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForClass(context.Background(), ws, "google", since)
				return err
			}},
		{"classe et domaine emetteur", `lower(split_part(veridian_sender_email, '@', 2)) = lower($3) `, []driver.Value{"google", since, "send.fr"},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForClassAndSenderDomain(context.Background(), ws, "google", "send.fr", since)
				return err
			}},
		{"domaines destinataires", `lower(split_part(contact_email, '@', 2)) = ANY($2)`, []driver.Value{since, sqlmock.AnyArg()},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForDomains(context.Background(), ws, []string{"gmail.com"}, false, since)
				return err
			}},
		{"domaines destinataires et domaine emetteur", `lower(split_part(veridian_sender_email, '@', 2)) = lower($3)`, []driver.Value{since, sqlmock.AnyArg(), "send.fr"},
			func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForDomainsAndSenderDomain(context.Background(), ws, []string{"gmail.com"}, false, "send.fr", since)
				return err
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockWS, repo, mock, db, cleanup := setupMessageHistoryTest(t)
			defer cleanup()
			mockWS.EXPECT().GetConnection(gomock.Any(), ws).Return(db, nil)
			mock.ExpectQuery(quoted(tc.expect) + `[\s\S]*` + quoted(commercialPredicate)).
				WithArgs(tc.args...).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
			require.NoError(t, tc.call(repo))
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMessageHistoryRepository_ReputationQueriesScopeByProfile(t *testing.T) {
	since := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	const ws = "workspace-123"
	scoped := `\(veridian_profile_id = \$3 OR \(COALESCE\(veridian_profile_id, ''\) = '' AND lower\(split_part\(veridian_sender_email, '@', 2\)\) = lower\(\$2\)\)\)`
	ctx := domain.WithVeridianReputationProfile(context.Background(), "profil-transac")

	t.Run("plaintes, rejets et envois d'un profil", func(t *testing.T) {
		for name, fn := range map[string]func(r domain.MessageHistoryRepository) error{
			"envois": func(r domain.MessageHistoryRepository) error {
				_, err := r.CountSentSinceForSenderDomain(ctx, ws, "send.fr", since)
				return err
			},
			"rejets": func(r domain.MessageHistoryRepository) error {
				_, err := r.CountHardBouncedSinceForSenderDomain(ctx, ws, "send.fr", since)
				return err
			},
			"plaintes": func(r domain.MessageHistoryRepository) error {
				_, err := r.CountComplainedSinceForSenderDomain(ctx, ws, "send.fr", since)
				return err
			},
		} {
			t.Run(name, func(t *testing.T) {
				mockWS, repo, mock, db, cleanup := setupMessageHistoryTest(t)
				defer cleanup()
				mockWS.EXPECT().GetConnection(gomock.Any(), ws).Return(db, nil)
				mock.ExpectQuery(scoped+`[\s\S]*`+quoted(commercialPredicate)).
					WithArgs(since, "send.fr", "profil-transac").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
				require.NoError(t, fn(repo))
				assert.NoError(t, mock.ExpectationsWereMet())
			})
		}
	})

	t.Run("ventilation par classe", func(t *testing.T) {
		mockWS, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()
		mockWS.EXPECT().GetConnection(gomock.Any(), ws).Return(db, nil)
		mock.ExpectQuery(scoped+`[\s\S]*`+quoted(commercialPredicate)+` GROUP BY 1`).
			WithArgs(since, "send.fr", "profil-transac").
			WillReturnRows(sqlmock.NewRows([]string{"c", "s", "h", "p"}).AddRow("ovh", 30, 1, 0))
		got, err := repo.ReputationCountsByClassSinceForSenderDomain(ctx, ws, "send.fr", since)
		require.NoError(t, err)
		assert.Equal(t, 30, got["ovh"].Sent)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("20 derniers envois du couple", func(t *testing.T) {
		mockWS, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()
		mockWS.EXPECT().GetConnection(gomock.Any(), ws).Return(db, nil)
		mock.ExpectQuery(`\(veridian_profile_id = \$5 OR \(COALESCE\(veridian_profile_id, ''\) = '' AND lower\(split_part\(veridian_sender_email, '@', 2\)\) = lower\(\$3\)\)\)[\s\S]*`+quoted(commercialPredicate)+` ORDER BY sent_at DESC LIMIT \$4`).
			WithArgs(since, "ovh", "send.fr", 20, "profil-transac").
			WillReturnRows(sqlmock.NewRows([]string{"s", "p"}).AddRow(20, 2))
		sent, policy, err := repo.RecentClassOutcomesForSenderDomain(ctx, ws, "send.fr", "ovh", 20, since)
		require.NoError(t, err)
		assert.Equal(t, 20, sent)
		assert.Equal(t, 2, policy)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("sans profil : le domaine seul, comme avant", func(t *testing.T) {
		mockWS, repo, mock, db, cleanup := setupMessageHistoryTest(t)
		defer cleanup()
		mockWS.EXPECT().GetConnection(gomock.Any(), ws).Return(db, nil)
		mock.ExpectQuery(`failed_at IS NULL AND lower\(split_part\(veridian_sender_email, '@', 2\)\) = lower\(\$2\) AND veridian_message_type`).
			WithArgs(since, "send.fr").
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
		_, err := repo.CountSentSinceForSenderDomain(context.Background(), ws, "send.fr", since)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
