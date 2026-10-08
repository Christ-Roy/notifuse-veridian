package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
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
	// Jour de compte Europe/Paris du 8 octobre 2026 (heure d'ete, UTC+2) : du 7 a
	// 22h00 UTC inclus au 8 a 22h00 UTC exclu, compteurs de la date civile 2026-10-08.
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)
	day := domain.VeridianDayAt(time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), paris)
	// Même filtre que les COUNT des portes du worker : envois du jour de compte, failed_at IS NULL.
	mock.ExpectQuery(`(?s)FROM message_history\s+WHERE sent_at >= \$1 AND sent_at < \$2 AND failed_at IS NULL\s+GROUP BY`).
		WithArgs(time.Date(2026, 10, 7, 22, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 22, 0, 0, 0, time.UTC)).
		WillReturnRows(sqlmock.NewRows([]string{"profile", "sender", "class", "type", "n"}).
			AddRow("p1", "hello@envoi.example", "google", "", 4).
			AddRow("", "notif@envoi.example", "", "transactional", 2))
	mock.ExpectQuery(`(?s)FROM veridian_daily_quota_counters\s+WHERE workspace_id = \$2 AND quota_day = \$1::date`).WithArgs("2026-10-08", "ws1").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "scope", "class", "used"}).
			AddRow("profile", "p1", "", 5).
			AddRow("provider_class", "envoi.example", "google", 4))
	repo := NewVeridianEmailProfileOverviewRepository(workspaceRepo)
	rows, counters, err := repo.GetPlanObservations(context.Background(), "ws1", day)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 4, rows[0].Accepted)
	assert.Equal(t, "google", rows[0].ProviderClass)
	assert.Equal(t, "transactional", rows[1].MessageType)
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

func TestVeridianEmailProfileOverviewRepositoryPropagatesQueryErrors(t *testing.T) {
	db, mock, cleanup := setupMockDB(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)
	mock.ExpectQuery(`FROM message_history`).WillReturnError(assert.AnError)
	repo := NewVeridianEmailProfileOverviewRepository(workspaceRepo)
	_, _, err := repo.GetPlanObservations(context.Background(), "ws1", domain.VeridianDayAt(time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan observations")
}
