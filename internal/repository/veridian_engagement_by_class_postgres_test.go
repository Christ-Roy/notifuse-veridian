package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

func TestNewVeridianEngagementByClassRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	repo := NewVeridianEngagementByClassRepository(workspaceRepo)
	require.NotNil(t, repo)
}

func TestGetEngagementByClass(t *testing.T) {
	engagementCols := []string{"class", "sent", "bounced"}
	replyCols := []string{"class", "replied_human"}

	setup := func(t *testing.T) (*mocks.MockWorkspaceRepository, sqlmock.Sqlmock, func()) {
		db, mock, cleanup := setupMockDB(t)
		ctrl := gomock.NewController(t)
		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil).AnyTimes()
		return workspaceRepo, mock, func() { ctrl.Finish(); cleanup() }
	}

	t.Run("groupe par classe persistee, jamais par suffixe de domaine ni created_at", func(t *testing.T) {
		workspaceRepo, mock, done := setup(t)
		defer done()
		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		mock.ExpectQuery(`SELECT COALESCE\(veridian_provider_class, ''\) AS class, COUNT\(\*\) FILTER \(WHERE sent_at IS NOT NULL\) AS sent, COUNT\(\*\) FILTER \(WHERE bounced_at IS NOT NULL\) AS bounced FROM message_history WHERE \(\(sent_at IS NOT NULL\) OR \(bounced_at IS NOT NULL\)\) GROUP BY 1`).
			WillReturnRows(sqlmock.NewRows(engagementCols).
				AddRow("ovh", 235, 14).
				AddRow("", 10, 0))
		mock.ExpectQuery(`FROM veridian_contact_reply r LEFT JOIN LATERAL .* WHERE r.reply_type = 'human' AND r.replied_at IS NOT NULL GROUP BY 1`).
			WillReturnRows(sqlmock.NewRows(replyCols).AddRow("ovh", 3))

		got, err := repo.GetEngagementByClass(context.Background(), "ws1", time.Time{}, time.Time{})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, domain.VeridianClassEngagementRow{Class: "ovh", Sent: 235, Bounced: 14, RepliedHuman: 3}, got[0])
		assert.Equal(t, "", got[1].Class)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("fenetre : sent_at, bounced_at et replied_at (jamais created_at)", func(t *testing.T) {
		workspaceRepo, mock, done := setup(t)
		defer done()
		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		since := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
		until := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

		mock.ExpectQuery(`FILTER \(WHERE sent_at IS NOT NULL AND sent_at >= \$1 AND sent_at < \$2\) AS sent, COUNT\(\*\) FILTER \(WHERE bounced_at IS NOT NULL AND bounced_at >= \$3 AND bounced_at < \$4\) AS bounced FROM message_history WHERE \(\(sent_at IS NOT NULL AND sent_at >= \$5 AND sent_at < \$6\) OR \(bounced_at IS NOT NULL AND bounced_at >= \$7 AND bounced_at < \$8\)\) GROUP BY 1`).
			WithArgs(since, until, since, until, since, until, since, until).
			WillReturnRows(sqlmock.NewRows(engagementCols).AddRow("google", 1, 0))
		mock.ExpectQuery(`r.replied_at IS NOT NULL AND r.replied_at >= \$1 AND r.replied_at < \$2 GROUP BY 1`).
			WithArgs(since, until).
			WillReturnRows(sqlmock.NewRows(replyCols))

		got, err := repo.GetEngagementByClass(context.Background(), "ws1", since, until)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("reponse d'une classe absente des envois -> ligne creee", func(t *testing.T) {
		workspaceRepo, mock, done := setup(t)
		defer done()
		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		mock.ExpectQuery(`FROM message_history`).WillReturnRows(sqlmock.NewRows(engagementCols))
		mock.ExpectQuery(`FROM veridian_contact_reply`).WillReturnRows(sqlmock.NewRows(replyCols).AddRow("ionos", 2))

		got, err := repo.GetEngagementByClass(context.Background(), "ws1", time.Time{}, time.Time{})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 2, got[0].RepliedHuman)
	})

	t.Run("connection error -> wrapped error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws-bad").Return(nil, errors.New("boom"))
		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		got, err := repo.GetEngagementByClass(context.Background(), "ws-bad", time.Time{}, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to get workspace connection")
	})

	t.Run("query error -> wrapped error", func(t *testing.T) {
		workspaceRepo, mock, done := setup(t)
		defer done()
		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		mock.ExpectQuery(`FROM message_history`).WillReturnError(errors.New("db down"))

		got, err := repo.GetEngagementByClass(context.Background(), "ws1", time.Time{}, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to execute engagement query")
	})

	t.Run("reply query error -> wrapped error", func(t *testing.T) {
		workspaceRepo, mock, done := setup(t)
		defer done()
		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		mock.ExpectQuery(`FROM message_history`).WillReturnRows(sqlmock.NewRows(engagementCols))
		mock.ExpectQuery(`FROM veridian_contact_reply`).WillReturnError(errors.New("no table"))

		_, err := repo.GetEngagementByClass(context.Background(), "ws1", time.Time{}, time.Time{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute reply query")
	})
}
