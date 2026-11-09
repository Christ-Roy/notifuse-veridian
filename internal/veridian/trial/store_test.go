package trial

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetTrial_EcritStatutEtDate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	ends := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	mock.ExpectExec(`UPDATE veridian_plan\s+SET trial_status = \$2, trial_ends_at = \$3`).
		WithArgs("ws1", "trial_active", &ends).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, (&Store{DB: db}).SetTrial(context.Background(), "ws1", "trial_active", &ends))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetTrial_WorkspaceSansPlan_ErrNoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`UPDATE veridian_plan`).WillReturnResult(sqlmock.NewResult(0, 0))
	err = (&Store{DB: db}).SetTrial(context.Background(), "ws-sans-plan", "none", nil)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestGetTrials_OmetLesWorkspacesSansStatut(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	ends := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`FROM veridian_plan\s+WHERE workspace_id = ANY\(\$1\) AND trial_status IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "trial_status", "trial_ends_at"}).
			AddRow("ws1", "trial_active", ends).AddRow("ws2", "paid", nil))

	got, err := (&Store{DB: db}).GetTrials(context.Background(), []string{"ws1", "ws2", "ws3"})
	require.NoError(t, err)
	require.Len(t, got, 2, "ws3 n'a jamais recu de statut: absent")
	assert.Equal(t, "trial_active", got["ws1"].Status)
	require.NotNil(t, got["ws1"].EndsAt)
	assert.True(t, got["ws1"].EndsAt.Equal(ends))
	assert.Equal(t, "paid", got["ws2"].Status)
	assert.Nil(t, got["ws2"].EndsAt)
}

func TestGetTrials_ListeVide_AucuneRequete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	got, err := (&Store{DB: db}).GetTrials(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetHubTenant_EcritEtErrNoRowsSansPlan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`UPDATE veridian_plan SET hub_tenant_id = \$2`).WithArgs("ws1", "359b76d5-0000").WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, (&Store{DB: db}).SetHubTenant(context.Background(), "ws1", "359b76d5-0000"))
	mock.ExpectExec(`UPDATE veridian_plan`).WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, (&Store{DB: db}).SetHubTenant(context.Background(), "ws-sans-plan", "x"), sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}
