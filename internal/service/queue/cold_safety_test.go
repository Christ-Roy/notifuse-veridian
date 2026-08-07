package queue

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validColdWorkspace() *domain.Workspace {
	return &domain.Workspace{
		ID: "robertbrunon",
		Settings: domain.WorkspaceSettings{
			Timezone:                        "Europe/Paris",
			VeridianColdSafetyEnabled:       true,
			VeridianProviderClassRates:      map[string]float64{"ovh": 0.1},
			VeridianProviderClassDailyCap:   map[string]int{"ovh": 10},
			VeridianWorkspaceDailyCap:       20,
			VeridianPerSenderDailyCap:       15,
			VeridianRecipientDomainDailyCap: 2,
			VeridianPerRecipientDailyCap:    1,
			VeridianExcludedProviderClasses: []string{"google"},
		},
	}
}

func validColdEntry() *domain.EmailQueueEntry {
	return &domain.EmailQueueEntry{
		ID:           "entry-1",
		MessageID:    "message-1",
		ContactEmail: "hello@example.fr",
		Payload: domain.EmailQueuePayload{
			FromAddress:   "sender@agences-veridian.fr",
			ProviderClass: "ovh",
		},
	}
}

func TestColdSafetyDisabledDoesNotTouchDatabases(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	guard := NewColdSafetyGuard(repo)
	workspace := validColdWorkspace()
	workspace.Settings.VeridianColdSafetyEnabled = false

	decision := guard.Reserve(context.Background(), workspace, validColdEntry())
	assert.True(t, decision.Allowed)
	assert.Equal(t, "cold_safety_disabled", decision.Reason)
}

func TestColdSafetyInvalidPolicyFailsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	guard := NewColdSafetyGuard(repo)
	workspace := validColdWorkspace()
	workspace.Settings.VeridianRecipientDomainDailyCap = 0

	decision := guard.Reserve(context.Background(), workspace, validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "invalid_policy")
}

func TestColdSafetyMissingQueueIdentityFailsClosedBeforeDatabases(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	guard := NewColdSafetyGuard(repo)

	entry := validColdEntry()
	entry.ID = ""
	decision := guard.Reserve(context.Background(), validColdWorkspace(), entry)
	assert.False(t, decision.Allowed)
	assert.Equal(t, "missing_queue_entry_id", decision.Reason)

	entry.ID = "entry-1"
	entry.MessageID = ""
	decision = guard.Reserve(context.Background(), validColdWorkspace(), entry)
	assert.False(t, decision.Allowed)
	assert.Equal(t, "missing_message_id", decision.Reason)
}

func TestColdSafetyUsesDurableGlobalSuppressionFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(db, nil)
	mock.ExpectQuery("SELECT status FROM veridian_global_suppressions").
		WithArgs(emailSHA256("hello@example.fr")).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("complained"))

	guard := NewColdSafetyGuard(repo)
	decision := guard.Reserve(context.Background(), validColdWorkspace(), validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Equal(t, "globally_suppressed:complained", decision.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestColdSafetyScansEveryWorkspaceAndPersistsSuppression(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	systemDB, systemMock, err := sqlmock.New()
	require.NoError(t, err)
	defer systemDB.Close()
	workspaceDB, workspaceMock, err := sqlmock.New()
	require.NoError(t, err)
	defer workspaceDB.Close()

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(systemDB, nil)
	systemMock.ExpectQuery("SELECT status FROM veridian_global_suppressions").
		WillReturnError(sql.ErrNoRows)
	repo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{{ID: "otherworkspace"}}, nil)
	repo.EXPECT().GetConnection(gomock.Any(), "otherworkspace").Return(workspaceDB, nil)
	workspaceMock.ExpectQuery("FROM contact_lists.*UNION ALL.*FROM message_history").
		WithArgs("hello@example.fr", "hello@example.fr").
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("bounced"))
	systemMock.ExpectExec("INSERT INTO veridian_global_suppressions").
		WithArgs(emailSHA256("hello@example.fr"), "bounced", "otherworkspace").
		WillReturnResult(sqlmock.NewResult(1, 1))

	guard := NewColdSafetyGuard(repo)
	decision := guard.Reserve(context.Background(), validColdWorkspace(), validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Equal(t, "globally_suppressed:bounced", decision.Reason)
	require.NoError(t, systemMock.ExpectationsWereMet())
	require.NoError(t, workspaceMock.ExpectationsWereMet())
}

func TestColdSafetyFailsClosedIfAnyWorkspaceSourceIsUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	systemDB, systemMock, err := sqlmock.New()
	require.NoError(t, err)
	defer systemDB.Close()

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(systemDB, nil)
	systemMock.ExpectQuery("SELECT status FROM veridian_global_suppressions").WillReturnError(sql.ErrNoRows)
	repo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{{ID: "broken"}}, nil)
	repo.EXPECT().GetConnection(gomock.Any(), "broken").Return(nil, errors.New("down"))

	guard := NewColdSafetyGuard(repo)
	decision := guard.Reserve(context.Background(), validColdWorkspace(), validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "global_suppression_unavailable")
}

func TestColdSafetyReservesAllCapsAtomically(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	fixed := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	settings := &validColdWorkspace().Settings

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(db, nil)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("robertbrunon:2026-08-07").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("robertbrunon", "entry-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("COUNT\\(\\*\\) FILTER \\(WHERE provider_class = \\$4\\).*WHERE previous.workspace_id = \\$1\\s+AND previous.provider_class = \\$4").
		WithArgs("robertbrunon", "2026-08-07", "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr")).
		WillReturnRows(sqlmock.NewRows([]string{"workspace", "sender", "provider", "domain", "recipient", "last"}).AddRow(0, 0, 0, 0, 0, nil))
	mock.ExpectExec("INSERT INTO veridian_send_reservations").
		WithArgs("robertbrunon", "entry-1", "message-1", 1, "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"), "2026-08-07", fixed).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	guard := NewColdSafetyGuard(repo)
	decision, err := guard.reserveQuota(
		context.Background(), "robertbrunon", "entry-1", "message-1", 1,
		"sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"),
		0.1, 10, settings, fixed,
	)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, "quota_reserved", decision.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestColdSafetyDomainCapDefersWithoutReservation(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	fixed := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	settings := &validColdWorkspace().Settings

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(db, nil)
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("COUNT\\(\\*\\) FILTER \\(WHERE provider_class = \\$4\\).*WHERE previous.workspace_id = \\$1\\s+AND previous.provider_class = \\$4").
		WillReturnRows(sqlmock.NewRows([]string{"workspace", "sender", "provider", "domain", "recipient", "last"}).AddRow(2, 2, 2, 2, 0, nil))
	mock.ExpectRollback()

	guard := NewColdSafetyGuard(repo)
	decision, err := guard.reserveQuota(
		context.Background(), "robertbrunon", "entry-1", "message-1", 1,
		"sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"),
		0.1, 10, settings, fixed,
	)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, "cap_reached:recipient_domain_daily:example.fr", decision.Reason)
	require.NoError(t, mock.ExpectationsWereMet())
}
