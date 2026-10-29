// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/contact_segment_queue_processor_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// TestContactSegmentQueueProcessor_ProcessQueue_CancelledContext verifies the
// entry gate: a caller whose context is already cancelled must not claim new
// work (the claim itself deliberately ignores cancellation once started), so
// ProcessQueue returns before even acquiring a connection.
func TestContactSegmentQueueProcessor_ProcessQueue_CancelledContext(t *testing.T) {
	t.Skip("écart non analysé (A1) : file de recalcul des segments, annulation / erreur / panique (le test attend une API de file que le fork n'a pas)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockContactSegmentQueueRepository(ctrl)
	mockSegmentRepo := mocks.NewMockSegmentRepository(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	processor := NewContactSegmentQueueProcessor(
		mockQueueRepo,
		mockSegmentRepo,
		mockContactRepo,
		mockWorkspaceRepo,
		mockLogger,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// No GetConnection expectation: the gate must short-circuit first.
	count, err := processor.ProcessQueue(ctx, "workspace1")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, count)
}

// TestContactSegmentQueueProcessor_ProcessQueue_ClaimError verifies that a
// failure of the atomic claim (DELETE ... RETURNING inside its short
// transaction) surfaces as an error, rolls back, and processes nothing.
func TestContactSegmentQueueProcessor_ProcessQueue_ClaimError(t *testing.T) {
	t.Skip("écart non analysé (A1) : file de recalcul des segments, annulation / erreur / panique (le test attend une API de file que le fork n'a pas)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockContactSegmentQueueRepository(ctrl)
	mockSegmentRepo := mocks.NewMockSegmentRepository(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	processor := NewContactSegmentQueueProcessor(
		mockQueueRepo,
		mockSegmentRepo,
		mockContactRepo,
		mockWorkspaceRepo,
		mockLogger,
	)

	ctx := context.Background()

	db, mock, err := sqlmock.New()
	var count int
	assert.NoError(t, err)
	defer func() { _ = db.Close() }()

	// The claim runs in a short transaction; a query failure rolls it back so
	// the rows survive for the next pass.
	mock.ExpectBegin()
	mock.ExpectQuery("DELETE FROM contact_segment_queue").
		WillReturnError(errors.New("claim failed"))
	mock.ExpectRollback()

	mockWorkspaceRepo.EXPECT().
		GetConnection(ctx, "workspace1").
		Return(db, nil)

	count, err = processor.ProcessQueue(ctx, "workspace1")
	assert.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "failed to claim pending emails")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestContactSegmentQueueProcessor_ProcessQueue_ProcessContactError verifies that
// a per-contact evaluation failure does not fail the batch: the contact is
// re-enqueued for retry (claimBatch already removed it) and the processed count
// reflects the failure.
func TestContactSegmentQueueProcessor_ProcessQueue_ProcessContactError(t *testing.T) {
	t.Skip("écart non analysé (A1) : file de recalcul des segments, annulation / erreur / panique (le test attend une API de file que le fork n'a pas)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockContactSegmentQueueRepository(ctrl)
	mockSegmentRepo := mocks.NewMockSegmentRepository(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	processor := NewContactSegmentQueueProcessor(
		mockQueueRepo,
		mockSegmentRepo,
		mockContactRepo,
		mockWorkspaceRepo,
		mockLogger,
	)

	ctx := context.Background()

	db, mock, err := sqlmock.New()
	var count int
	assert.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Setup segment
	sql := "SELECT email FROM contacts WHERE email LIKE $1"
	segment := &domain.Segment{
		ID:            "segment1",
		Name:          "Test Segment",
		Status:        string(domain.SegmentStatusActive),
		Version:       1,
		GeneratedSQL:  &sql,
		GeneratedArgs: domain.JSONArray{"%test%"},
	}

	// Claim one contact.
	mock.ExpectBegin()
	emailRows := sqlmock.NewRows([]string{"email"}).AddRow("test@test.com")
	mock.ExpectQuery("DELETE FROM contact_segment_queue").WillReturnRows(emailRows)
	mock.ExpectCommit()

	// Segment evaluation query fails -> processContact returns an error.
	mock.ExpectQuery("SELECT 'segment1' as segment_id").
		WillReturnError(errors.New("eval failed"))

	// The failed contact is re-enqueued.
	mock.ExpectExec("INSERT INTO contact_segment_queue").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mockWorkspaceRepo.EXPECT().
		GetConnection(ctx, "workspace1").
		Return(db, nil)

	mockSegmentRepo.EXPECT().
		GetSegments(ctx, "workspace1", false).
		Return([]*domain.Segment{segment}, nil)

	count, err = processor.ProcessQueue(ctx, "workspace1")
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestContactSegmentQueueProcessor_ProcessQueue_ProcessContactPanic_RecoveredAndRequeued
// verifies the per-contact panic containment: a panic evaluating one contact is
// recovered (this is a permanent recurring task — crashing would re-claim the
// same contact after the debounce and crash again, forever), the contact takes
// the failed-contact requeue path, and ProcessQueue returns normally.
func TestContactSegmentQueueProcessor_ProcessQueue_ProcessContactPanic_RecoveredAndRequeued(t *testing.T) {
	t.Skip("écart non analysé (A1) : file de recalcul des segments, annulation / erreur / panique (le test attend une API de file que le fork n'a pas)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockContactSegmentQueueRepository(ctrl)
	mockSegmentRepo := mocks.NewMockSegmentRepository(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	processor := NewContactSegmentQueueProcessor(
		mockQueueRepo,
		mockSegmentRepo,
		mockContactRepo,
		mockWorkspaceRepo,
		mockLogger,
	)

	ctx := context.Background()

	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Non-empty GeneratedSQL is required so processContact reaches the
	// membership write (an empty one returns before any segment call).
	sql := "SELECT email FROM contacts WHERE email LIKE $1"
	segment := &domain.Segment{
		ID:            "segment1",
		Name:          "Test Segment",
		Status:        string(domain.SegmentStatusActive),
		Version:       1,
		GeneratedSQL:  &sql,
		GeneratedArgs: domain.JSONArray{"%test%"},
	}

	// Claim one contact.
	mock.ExpectBegin()
	emailRows := sqlmock.NewRows([]string{"email"}).AddRow("test@test.com")
	mock.ExpectQuery("DELETE FROM contact_segment_queue").WillReturnRows(emailRows)
	mock.ExpectCommit()

	// Eval matches segment1 so the panicking AddContactToSegment is reached.
	segmentRows := sqlmock.NewRows([]string{"segment_id"}).AddRow("segment1")
	mock.ExpectQuery("SELECT 'segment1' as segment_id").WillReturnRows(segmentRows)

	// The panicked contact is re-enqueued via the failed-contact path.
	mock.ExpectExec("INSERT INTO contact_segment_queue").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mockWorkspaceRepo.EXPECT().
		GetConnection(ctx, "workspace1").
		Return(db, nil)

	mockSegmentRepo.EXPECT().
		GetSegments(ctx, "workspace1", false).
		Return([]*domain.Segment{segment}, nil)

	mockSegmentRepo.EXPECT().
		AddContactToSegment(ctx, "workspace1", "test@test.com", "segment1", int64(1)).
		Do(func(context.Context, string, string, string, int64) {
			panic("boom")
		}).
		Return(nil)

	var count int
	assert.NotPanics(t, func() {
		count, err = processor.ProcessQueue(ctx, "workspace1")
	})
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestContactSegmentQueueProcessor_ProcessQueue_CancelledMidBatch_RequeuesRemainder
// verifies the cancellation path INSIDE the batch: when the caller's context is
// cancelled after the claim but before per-contact work, every unprocessed
// contact must be re-enqueued (the claim already deleted the rows) and no
// contact may be evaluated. The requeue itself must succeed despite the
// cancelled parent: it runs on a detached context — with a non-detached one,
// database/sql would reject the Exec before it reached the driver, so the
// requeue expectation below also pins the detachment.
func TestContactSegmentQueueProcessor_ProcessQueue_CancelledMidBatch_RequeuesRemainder(t *testing.T) {
	t.Skip("écart non analysé (A1) : file de recalcul des segments, annulation / erreur / panique (le test attend une API de file que le fork n'a pas)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockContactSegmentQueueRepository(ctrl)
	mockSegmentRepo := mocks.NewMockSegmentRepository(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	processor := NewContactSegmentQueueProcessor(
		mockQueueRepo,
		mockSegmentRepo,
		mockContactRepo,
		mockWorkspaceRepo,
		mockLogger,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer func() { _ = db.Close() }()

	sql := "SELECT email FROM contacts WHERE email LIKE $1"
	segment := &domain.Segment{
		ID:            "segment1",
		Name:          "Test Segment",
		Status:        string(domain.SegmentStatusActive),
		Version:       1,
		GeneratedSQL:  &sql,
		GeneratedArgs: domain.JSONArray{"%test%"},
	}

	// Claim two contacts.
	mock.ExpectBegin()
	emailRows := sqlmock.NewRows([]string{"email"}).
		AddRow("a@test.com").
		AddRow("b@test.com")
	mock.ExpectQuery("DELETE FROM contact_segment_queue").WillReturnRows(emailRows)
	mock.ExpectCommit()

	// No eval query is expected between commit and requeue: after the
	// cancellation below, the loop must not process any contact.

	// The whole claimed batch is re-enqueued.
	mock.ExpectExec("INSERT INTO contact_segment_queue").
		WithArgs("a@test.com", "b@test.com").
		WillReturnResult(sqlmock.NewResult(0, 2))

	mockWorkspaceRepo.EXPECT().
		GetConnection(ctx, "workspace1").
		Return(db, nil)

	// Cancel between the claim and the per-contact loop: GetSegments runs after
	// claimBatch commits, so cancelling here lands exactly in that window.
	mockSegmentRepo.EXPECT().
		GetSegments(ctx, "workspace1", false).
		Do(func(context.Context, string, bool) { cancel() }).
		Return([]*domain.Segment{segment}, nil)

	count, err := processor.ProcessQueue(ctx, "workspace1")
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "no contact may be counted processed after cancellation")
	assert.NoError(t, mock.ExpectationsWereMet())
}
