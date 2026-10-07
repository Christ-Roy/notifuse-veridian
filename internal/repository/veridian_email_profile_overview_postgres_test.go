package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianEmailProfileOverviewRepository(t *testing.T) {
	db, mock, cleanup := setupMockDB(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)
	since := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	// Même filtre que les COUNT des portes du worker : sent_at >= minuit, failed_at IS NULL.
	mock.ExpectQuery(`(?s)FROM message_history\s+WHERE sent_at >= \$1 AND failed_at IS NULL\s+GROUP BY`).WithArgs(since).
		WillReturnRows(sqlmock.NewRows([]string{"profile", "sender", "class", "n"}).
			AddRow("p1", "hello@envoi.example", "google", 4).
			AddRow("", "notif@envoi.example", "", 2))
	mock.ExpectQuery(`(?s)FROM veridian_daily_quota_counters\s+WHERE workspace_id = \$2`).WithArgs(since, "ws1").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "scope", "class", "used"}).
			AddRow("profile", "p1", "", 5).
			AddRow("provider_class", "envoi.example", "google", 4))
	repo := NewVeridianEmailProfileOverviewRepository(workspaceRepo)
	rows, counters, err := repo.GetPlanObservations(context.Background(), "ws1", since)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 4, rows[0].Accepted)
	assert.Equal(t, "google", rows[0].ProviderClass)
	require.Len(t, counters, 2)
	assert.Equal(t, 5, counters[0].Used)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewVeridianEmailProfileOverviewRepositoryRetainsWorkspaceRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	repo := NewVeridianEmailProfileOverviewRepository(workspaceRepo)
	concrete, ok := repo.(*veridianEmailProfileOverviewRepository)
	require.True(t, ok)
	assert.Same(t, workspaceRepo, concrete.workspaceRepo)
}
