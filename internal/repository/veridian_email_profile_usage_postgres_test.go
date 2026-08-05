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

func TestVeridianEmailProfileUsageRepository(t *testing.T) {
	db, mock, cleanup := setupMockDB(t)
	defer cleanup()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws1").Return(db, nil)
	since := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	// Defense in depth: even tenant-local databases must scope the authoritative
	// counter query, so a foreign workspace row can never be reported.
	mock.ExpectQuery(`(?s)SELECT sender_domain AS profile_id.*WHERE workspace_id = \$2`).WithArgs(since, "ws1").
		WillReturnRows(sqlmock.NewRows([]string{"profile", "class", "reserved_used", "accepted_used"}).
			AddRow("p1", "", 5, 0).
			AddRow("p1", "google", 0, 4))
	repo := NewVeridianEmailProfileUsageRepository(workspaceRepo)
	rows, err := repo.GetEmailProfileUsage(context.Background(), "ws1", since)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 5, rows[0].ReservedUsed)
	assert.Equal(t, 4, rows[1].AcceptedUsed)
	assert.NoError(t, mock.ExpectationsWereMet())
}
