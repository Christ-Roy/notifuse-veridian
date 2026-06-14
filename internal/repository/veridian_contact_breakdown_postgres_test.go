package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

func TestNewVeridianContactBreakdownRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	repo := NewVeridianContactBreakdownRepository(workspaceRepo)
	require.NotNil(t, repo)

	// La DP workspaceRepo doit être branchée : un appel passe par GetConnection.
	db, mock, cleanup := setupMockDB(t)
	defer cleanup()
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws-wire").Return(db, nil)
	mock.ExpectQuery(`SELECT c\.email, c\.custom_string_5 FROM contacts c`).
		WillReturnRows(sqlmock.NewRows([]string{"email", "custom_string_5"}))

	got, err := repo.GetProviderClassRows(context.Background(), "ws-wire", "")
	require.NoError(t, err)
	require.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProviderClassRows(t *testing.T) {
	t.Run("no list -> selects email + custom_string_5 for whole workspace", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "workspace123").Return(db, nil).AnyTimes()

		repo := NewVeridianContactBreakdownRepository(workspaceRepo)

		rows := sqlmock.NewRows([]string{"email", "custom_string_5"}).
			AddRow("jane@gmail.com", nil).
			AddRow("bob@acme.io", "microsoft")

		mock.ExpectQuery(`SELECT c\.email, c\.custom_string_5 FROM contacts c`).
			WillReturnRows(rows)

		got, err := repo.GetProviderClassRows(context.Background(), "workspace123", "")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "jane@gmail.com", got[0].Email)
		assert.Nil(t, got[0].CustomString5)
		assert.Equal(t, "bob@acme.io", got[1].Email)
		require.NotNil(t, got[1].CustomString5)
		assert.Equal(t, "microsoft", got[1].CustomString5.String)
		assert.False(t, got[1].CustomString5.IsNull)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("with list_id -> adds EXISTS subquery filter", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "workspace123").Return(db, nil).AnyTimes()

		repo := NewVeridianContactBreakdownRepository(workspaceRepo)

		rows := sqlmock.NewRows([]string{"email", "custom_string_5"}).
			AddRow("x@yahoo.fr", nil)

		mock.ExpectQuery(`SELECT c\.email, c\.custom_string_5 FROM contacts c WHERE EXISTS \(SELECT 1 FROM contact_lists cl WHERE cl\.email = c\.email AND cl\.deleted_at IS NULL AND cl\.list_id = \$1\)`).
			WithArgs("list-abc").
			WillReturnRows(rows)

		got, err := repo.GetProviderClassRows(context.Background(), "workspace123", "list-abc")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "x@yahoo.fr", got[0].Email)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result -> nil slice, no error", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "workspace123").Return(db, nil).AnyTimes()

		repo := NewVeridianContactBreakdownRepository(workspaceRepo)

		mock.ExpectQuery(`SELECT c\.email, c\.custom_string_5 FROM contacts c`).
			WillReturnRows(sqlmock.NewRows([]string{"email", "custom_string_5"}))

		got, err := repo.GetProviderClassRows(context.Background(), "workspace123", "")
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

		repo := NewVeridianContactBreakdownRepository(workspaceRepo)

		got, err := repo.GetProviderClassRows(context.Background(), "ws-bad", "")
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to get workspace connection")
	})
}
