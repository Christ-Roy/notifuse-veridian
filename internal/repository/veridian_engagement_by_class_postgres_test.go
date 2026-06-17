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

	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

func TestNewVeridianEngagementByClassRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	repo := NewVeridianEngagementByClassRepository(workspaceRepo)
	require.NotNil(t, repo)
}

func TestGetEngagementByDomain(t *testing.T) {
	engagementCols := []string{"domain", "sent", "delivered", "bounced", "opened", "clicked"}

	t.Run("no bounds -> groups by domain, no created_at filter", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil).AnyTimes()

		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		rows := sqlmock.NewRows(engagementCols).
			AddRow("gmail.com", 100, 95, 3, 40, 10).
			AddRow("acme.example", 7, 6, 1, 2, 0)

		mock.ExpectQuery(`SELECT lower\(split_part\(contact_email, '@', 2\)\) AS domain, COUNT\(\*\) FILTER \(WHERE sent_at IS NOT NULL\) AS sent, COUNT\(\*\) FILTER \(WHERE delivered_at IS NOT NULL\) AS delivered, COUNT\(\*\) FILTER \(WHERE bounced_at IS NOT NULL\) AS bounced, COUNT\(\*\) FILTER \(WHERE opened_at IS NOT NULL\) AS opened, COUNT\(\*\) FILTER \(WHERE clicked_at IS NOT NULL\) AS clicked FROM message_history GROUP BY 1`).
			WillReturnRows(rows)

		got, err := repo.GetEngagementByDomain(context.Background(), "ws1", time.Time{}, time.Time{})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "gmail.com", got[0].Domain)
		assert.Equal(t, 100, got[0].Sent)
		assert.Equal(t, 3, got[0].Bounced)
		assert.Equal(t, "acme.example", got[1].Domain)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("with both bounds -> adds created_at >= and < filters", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil).AnyTimes()

		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		until := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)

		mock.ExpectQuery(`FROM message_history WHERE created_at >= \$1 AND created_at < \$2 GROUP BY 1`).
			WithArgs(since, until).
			WillReturnRows(sqlmock.NewRows(engagementCols).AddRow("gmail.com", 5, 5, 0, 1, 0))

		got, err := repo.GetEngagementByDomain(context.Background(), "ws1", since, until)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result -> nil slice, no error", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil).AnyTimes()

		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		mock.ExpectQuery(`FROM message_history GROUP BY 1`).
			WillReturnRows(sqlmock.NewRows(engagementCols))

		got, err := repo.GetEngagementByDomain(context.Background(), "ws1", time.Time{}, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("connection error -> wrapped error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws-bad").
			Return(nil, errors.New("boom"))

		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		got, err := repo.GetEngagementByDomain(context.Background(), "ws-bad", time.Time{}, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to get workspace connection")
	})

	t.Run("query error -> wrapped error", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil).AnyTimes()

		repo := NewVeridianEngagementByClassRepository(workspaceRepo)

		mock.ExpectQuery(`FROM message_history GROUP BY 1`).
			WillReturnError(errors.New("db down"))

		got, err := repo.GetEngagementByDomain(context.Background(), "ws1", time.Time{}, time.Time{})
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to execute engagement query")
	})
}
