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
	mock.ExpectQuery(`SELECT lower\(split_part\(c\.email, '@', 2\)\) AS domain, c\.custom_string_5, COUNT\(\*\) AS cnt FROM contacts c GROUP BY`).
		WillReturnRows(sqlmock.NewRows([]string{"domain", "custom_string_5", "cnt"}))

	got, err := repo.GetProviderClassCounts(context.Background(), "ws-wire", "")
	require.NoError(t, err)
	require.Empty(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProviderClassCounts(t *testing.T) {
	t.Run("no list -> GROUP BY domain+tag for whole workspace (pas de SELECT non borné)", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "workspace123").Return(db, nil).AnyTimes()

		repo := NewVeridianContactBreakdownRepository(workspaceRepo)

		// La requête agrège côté SQL : domaine pré-extrait + tag + count.
		rows := sqlmock.NewRows([]string{"domain", "custom_string_5", "cnt"}).
			AddRow("gmail.com", nil, 42).
			AddRow("acme.io", "microsoft", 7)

		mock.ExpectQuery(`SELECT lower\(split_part\(c\.email, '@', 2\)\) AS domain, c\.custom_string_5, COUNT\(\*\) AS cnt FROM contacts c GROUP BY lower\(split_part\(c\.email, '@', 2\)\), c\.custom_string_5`).
			WillReturnRows(rows)

		got, err := repo.GetProviderClassCounts(context.Background(), "workspace123", "")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "gmail.com", got[0].Domain)
		assert.Equal(t, 42, got[0].Count)
		assert.Nil(t, got[0].CustomString5)
		assert.Equal(t, "acme.io", got[1].Domain)
		assert.Equal(t, 7, got[1].Count)
		require.NotNil(t, got[1].CustomString5)
		assert.Equal(t, "microsoft", got[1].CustomString5.String)
		assert.False(t, got[1].CustomString5.IsNull)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("with list_id -> adds EXISTS subquery filter avant le GROUP BY", func(t *testing.T) {
		db, mock, cleanup := setupMockDB(t)
		defer cleanup()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
		workspaceRepo.EXPECT().GetConnection(gomock.Any(), "workspace123").Return(db, nil).AnyTimes()

		repo := NewVeridianContactBreakdownRepository(workspaceRepo)

		rows := sqlmock.NewRows([]string{"domain", "custom_string_5", "cnt"}).
			AddRow("yahoo.fr", nil, 3)

		mock.ExpectQuery(`SELECT lower\(split_part\(c\.email, '@', 2\)\) AS domain, c\.custom_string_5, COUNT\(\*\) AS cnt FROM contacts c WHERE EXISTS \(SELECT 1 FROM contact_lists cl WHERE cl\.email = c\.email AND cl\.deleted_at IS NULL AND cl\.list_id = \$1\) GROUP BY lower\(split_part\(c\.email, '@', 2\)\), c\.custom_string_5`).
			WithArgs("list-abc").
			WillReturnRows(rows)

		got, err := repo.GetProviderClassCounts(context.Background(), "workspace123", "list-abc")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "yahoo.fr", got[0].Domain)
		assert.Equal(t, 3, got[0].Count)

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

		mock.ExpectQuery(`SELECT lower\(split_part\(c\.email, '@', 2\)\) AS domain, c\.custom_string_5, COUNT\(\*\) AS cnt FROM contacts c GROUP BY`).
			WillReturnRows(sqlmock.NewRows([]string{"domain", "custom_string_5", "cnt"}))

		got, err := repo.GetProviderClassCounts(context.Background(), "workspace123", "")
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

		got, err := repo.GetProviderClassCounts(context.Background(), "ws-bad", "")
		assert.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to get workspace connection")
	})
}
