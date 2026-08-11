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
			FromAddress:           "sender@agences-veridian.fr",
			VeridianProviderClass: "ovh",
		},
	}
}

func TestColdSafetyDisabledDoesNotTouchDatabases(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	guard := NewColdSafetyGuard(repo)
	workspace := validColdWorkspace()
	workspace.Settings.VeridianColdSafetyEnabled = false

	decision, lease := guard.Authorize(context.Background(), workspace, validColdEntry())
	assert.True(t, decision.Allowed)
	assert.Equal(t, "cold_safety_disabled", decision.Reason)
	assert.Nil(t, lease)
}

func TestColdSafetyInvalidPolicyFailsClosed(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	guard := NewColdSafetyGuard(repo)
	workspace := validColdWorkspace()
	workspace.Settings.VeridianRecipientDomainDailyCap = 0

	decision, lease := guard.Authorize(context.Background(), workspace, validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "invalid_policy")
	assert.Nil(t, lease)
}

func TestColdSafetyUsesDurableGlobalSuppressionFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(db, nil)
	mock.ExpectQuery("SELECT status FROM veridian_global_suppressions").WithArgs(emailSHA256("hello@example.fr")).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("complained"))

	decision, lease := NewColdSafetyGuard(repo).Authorize(context.Background(), validColdWorkspace(), validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Equal(t, "globally_suppressed:complained", decision.Reason)
	assert.Nil(t, lease)
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
	systemMock.ExpectQuery("SELECT status FROM veridian_global_suppressions").WillReturnError(sql.ErrNoRows)
	repo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{{ID: "otherworkspace"}}, nil)
	repo.EXPECT().GetConnection(gomock.Any(), "otherworkspace").Return(workspaceDB, nil)
	workspaceMock.ExpectQuery("FROM contact_lists.*UNION ALL.*FROM message_history").WithArgs("hello@example.fr", "hello@example.fr").WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("bounced"))
	systemMock.ExpectExec("INSERT INTO veridian_global_suppressions").WithArgs(emailSHA256("hello@example.fr"), "bounced", "otherworkspace").WillReturnResult(sqlmock.NewResult(1, 1))

	decision, lease := NewColdSafetyGuard(repo).Authorize(context.Background(), validColdWorkspace(), validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Equal(t, "globally_suppressed:bounced", decision.Reason)
	assert.Nil(t, lease)
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

	decision, lease := NewColdSafetyGuard(repo).Authorize(context.Background(), validColdWorkspace(), validColdEntry())
	assert.False(t, decision.Allowed)
	assert.Contains(t, decision.Reason, "global_suppression_unavailable")
	assert.Nil(t, lease)
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
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("robertbrunon:2026-08-07").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT EXISTS").WithArgs("robertbrunon", "entry-1", 1, "2026-08-07").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("COUNT\\(\\*\\) FILTER \\(WHERE provider_class = \\$4\\).*WHERE previous.workspace_id = \\$1\\s+AND previous.provider_class = \\$4").WithArgs("robertbrunon", "2026-08-07", "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr")).WillReturnRows(sqlmock.NewRows([]string{"workspace", "sender", "provider", "domain", "recipient", "last"}).AddRow(0, 0, 0, 0, 0, nil))
	mock.ExpectExec("INSERT INTO veridian_send_reservations").WithArgs("robertbrunon", "entry-1", "message-1", 1, "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"), "2026-08-07", fixed).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	guard := NewColdSafetyGuard(repo)
	guard.now = func() time.Time { return fixed }
	decision, lease, err := guard.reserveQuota(context.Background(), "robertbrunon", "entry-1", "message-1", 1, "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"), 0.1, 10, settings)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	require.NotNil(t, lease)
	assert.True(t, lease.created)
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
	mock.ExpectQuery("COUNT\\(\\*\\) FILTER \\(WHERE provider_class = \\$4\\).*WHERE previous.workspace_id = \\$1\\s+AND previous.provider_class = \\$4").WillReturnRows(sqlmock.NewRows([]string{"workspace", "sender", "provider", "domain", "recipient", "last"}).AddRow(2, 2, 2, 2, 0, nil))
	mock.ExpectRollback()

	guard := NewColdSafetyGuard(repo)
	guard.now = func() time.Time { return fixed }
	decision, lease, err := guard.reserveQuota(context.Background(), "robertbrunon", "entry-1", "message-1", 1, "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"), 0.1, 10, settings)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, "cap_reached:recipient_domain_daily:example.fr", decision.Reason)
	assert.Nil(t, lease)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestColdSafetyReleaseDeletesOnlyCreatedLease(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo.EXPECT().GetSystemConnection(gomock.Any()).Return(db, nil)
	mock.ExpectExec("DELETE FROM veridian_send_reservations").WithArgs("robertbrunon", "entry-1", 1, "2026-08-07").WillReturnResult(sqlmock.NewResult(0, 1))

	guard := NewColdSafetyGuard(repo)
	require.NoError(t, guard.Release(context.Background(), &ColdSafetyLease{workspaceID: "robertbrunon", queueEntryID: "entry-1", attempt: 1, quotaDate: "2026-08-07", created: true}))
	require.NoError(t, guard.Release(context.Background(), &ColdSafetyLease{created: false}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEmailQueueWorkerColdSafetySuppressionBlocksSMTPAndRefundsClaim(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	systemDB, systemMock, err := sqlmock.New()
	require.NoError(t, err)
	defer systemDB.Close()

	workspace := validColdWorkspace()
	workspace.ID = "ws-1"
	workspace.Integrations = []domain.Integration{{
		ID: "int-1",
		EmailProvider: domain.EmailProvider{
			Kind:               domain.EmailProviderKindSMTP,
			RateLimitPerMinute: 6000,
		},
	}}
	entry := veridianTestEntryFrom(
		"cold-suppressed",
		"hello@example.fr",
		"sender@agences-veridian.fr",
		domain.EmailQueuePayload{VeridianProviderClass: "ovh"},
	)

	// Legacy pre-claim optimizations see capacity. The durable v57 suppression
	// lookup is the authoritative last-mile denial after the queue claim.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "hello@example.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws-1", gomock.Any(), false, "agences-veridian.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "sender@agences-veridian.fr", gomock.Any()).
		Return(0, nil)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", entry.ID).Return(nil)
	env.mockWorkspaceRepo.EXPECT().GetSystemConnection(gomock.Any()).Return(systemDB, nil)
	systemMock.ExpectQuery("SELECT status FROM veridian_global_suppressions").
		WithArgs(emailSHA256("hello@example.fr")).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("complained"))
	env.mockQueueRepo.EXPECT().
		SetNextRetryAndRefundAttempt(gomock.Any(), "ws-1", entry.ID, gomock.Any()).
		Return(nil)

	// No SendEmail expectation: any SMTP attempt fails the test.
	env.worker.processEntry(workspace, entry)
	require.NoError(t, systemMock.ExpectationsWereMet())
}

func TestEmailQueueWorkerReleasesColdSafetyLeaseWhenV55QuotaBlocks(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	systemDB, systemMock, err := sqlmock.New()
	require.NoError(t, err)
	defer systemDB.Close()
	workspaceDB, workspaceMock, err := sqlmock.New()
	require.NoError(t, err)
	defer workspaceDB.Close()

	fixed := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	env.worker.coldSafetyGuard.now = func() time.Time { return fixed }
	workspace := validColdWorkspace()
	workspace.ID = "ws-1"
	workspace.Integrations = []domain.Integration{{
		ID: "int-1",
		EmailProvider: domain.EmailProvider{
			Kind:               domain.EmailProviderKindSMTP,
			RateLimitPerMinute: 6000,
		},
	}}
	entry := veridianTestEntryFrom(
		"cold-v55-block",
		"hello@example.fr",
		"sender@agences-veridian.fr",
		domain.EmailQueuePayload{VeridianProviderClass: "ovh"},
	)
	quotaRepo := &quotaTestRepository{
		MessageHistoryRepository: env.mockMessageHistoryRepo,
		outcomes: map[string]quotaTestOutcome{
			domain.VeridianDailyQuotaKindProviderClass: {
				result: domain.VeridianDailyQuotaReservationResult{Reserved: false, Used: 10},
			},
		},
	}
	env.worker.messageHistoryRepo = quotaRepo

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "hello@example.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws-1", gomock.Any(), false, "agences-veridian.fr", gomock.Any()).
		Return(0, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", "sender@agences-veridian.fr", gomock.Any()).
		Return(0, nil)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", entry.ID).Return(nil)

	env.mockWorkspaceRepo.EXPECT().GetSystemConnection(gomock.Any()).Return(systemDB, nil).Times(3)
	systemMock.ExpectQuery("SELECT status FROM veridian_global_suppressions").
		WillReturnError(sql.ErrNoRows)
	env.mockWorkspaceRepo.EXPECT().List(gomock.Any()).Return([]*domain.Workspace{{ID: "ws-1"}}, nil)
	env.mockWorkspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws-1").Return(workspaceDB, nil)
	workspaceMock.ExpectQuery("FROM contact_lists.*UNION ALL.*FROM message_history").
		WithArgs("hello@example.fr", "hello@example.fr").
		WillReturnError(sql.ErrNoRows)

	systemMock.ExpectBegin()
	systemMock.ExpectExec("SELECT pg_advisory_xact_lock").
		WithArgs("ws-1:2026-08-07").
		WillReturnResult(sqlmock.NewResult(0, 1))
	systemMock.ExpectQuery("SELECT EXISTS").
		WithArgs("ws-1", entry.ID, 1, "2026-08-07").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	systemMock.ExpectQuery("COUNT\\(\\*\\) FILTER \\(WHERE provider_class = \\$4\\)").
		WithArgs("ws-1", "2026-08-07", "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr")).
		WillReturnRows(sqlmock.NewRows([]string{"workspace", "sender", "provider", "domain", "recipient", "last"}).AddRow(0, 0, 0, 0, 0, nil))
	systemMock.ExpectExec("INSERT INTO veridian_send_reservations").
		WithArgs("ws-1", entry.ID, entry.MessageID, 1, "sender@agences-veridian.fr", "ovh", "example.fr", emailSHA256("hello@example.fr"), "2026-08-07", fixed).
		WillReturnResult(sqlmock.NewResult(1, 1))
	systemMock.ExpectCommit()
	systemMock.ExpectExec("DELETE FROM veridian_send_reservations").
		WithArgs("ws-1", entry.ID, 1, "2026-08-07").
		WillReturnResult(sqlmock.NewResult(0, 1))
	env.mockQueueRepo.EXPECT().
		SetNextRetryAndRefundAttempt(gomock.Any(), "ws-1", entry.ID, gomock.Any()).
		Return(nil)

	// V55 denies after v57 reserved. The worker must delete the v57 lease,
	// refund the queue claim and never touch SMTP.
	env.worker.processEntry(workspace, entry)
	require.Equal(t, []string{domain.VeridianDailyQuotaKindProviderClass}, quotaRepo.reserved)
	require.NoError(t, systemMock.ExpectationsWereMet())
	require.NoError(t, workspaceMock.ExpectationsWereMet())
}
