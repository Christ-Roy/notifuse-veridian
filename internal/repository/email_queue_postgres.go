package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

// EmailQueueRepository implements domain.EmailQueueRepository
type EmailQueueRepository struct {
	workspaceRepo domain.WorkspaceRepository
	db            *sql.DB // Used for testing with sqlmock

	// fairRotation makes the round robin of FetchPending open with a different
	// source at each call (cf. domain.VeridianSelectFairBatch).
	fairRotation atomic.Uint64
}

// NewEmailQueueRepository creates a new EmailQueueRepository using workspace repository
func NewEmailQueueRepository(workspaceRepo domain.WorkspaceRepository) domain.EmailQueueRepository {
	return &EmailQueueRepository{
		workspaceRepo: workspaceRepo,
	}
}

// NewEmailQueueRepositoryWithDB creates a new EmailQueueRepository with a direct DB connection (for testing)
func NewEmailQueueRepositoryWithDB(db *sql.DB) domain.EmailQueueRepository {
	return &EmailQueueRepository{
		db: db,
	}
}

// getDB returns the database connection for a workspace
func (r *EmailQueueRepository) getDB(ctx context.Context, workspaceID string) (*sql.DB, error) {
	if r.db != nil {
		return r.db, nil
	}
	return r.workspaceRepo.GetConnection(ctx, workspaceID)
}

// psql is a Squirrel StatementBuilder configured for PostgreSQL
var emailQueuePsql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Enqueue adds emails to the queue
func (r *EmailQueueRepository) Enqueue(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
	if len(entries) == 0 {
		return nil
	}

	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	// Use a transaction for batch insert
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := r.EnqueueTx(ctx, tx, workspaceID, entries); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// EnqueueTx adds emails to the queue within an existing transaction
func (r *EmailQueueRepository) EnqueueTx(ctx context.Context, tx *sql.Tx, workspaceID string, entries []*domain.EmailQueueEntry) error {
	if len(entries) == 0 {
		return nil
	}

	// Serialize queue inserts with integration deletion. The deletion guard takes
	// SHARE; this ROW EXCLUSIVE lock makes the winner observable to the loser.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE email_queue IN ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("failed to lock email queue for enqueue: %w", err)
	}

	// Re-read the workspace only after acquiring the queue lock. If deletion won
	// the race, the integration is already absent and no stale row is inserted.
	// The direct-DB constructor is test-only and deliberately has no workspace repo.
	if r.workspaceRepo != nil {
		workspace, err := r.workspaceRepo.GetByID(ctx, workspaceID)
		if err != nil {
			return fmt.Errorf("failed to validate queue email integration: %w", err)
		}
		validated := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			if _, ok := validated[entry.IntegrationID]; ok {
				continue
			}
			integration := workspace.GetIntegrationByID(entry.IntegrationID)
			if integration == nil || integration.Type != domain.IntegrationTypeEmail {
				return fmt.Errorf("email integration %q is not configured in workspace %q", entry.IntegrationID, workspaceID)
			}
			validated[entry.IntegrationID] = struct{}{}
		}
	}

	now := time.Now().UTC()

	insertBuilder := emailQueuePsql.
		Insert("email_queue").
		Columns(
			"id", "status", "priority", "source_type", "source_id",
			"integration_id", "provider_kind", "contact_email", "message_id",
			"template_id", "payload", "attempts", "max_attempts",
			"created_at", "updated_at", "node_id",
		)

	for _, entry := range entries {
		// Generate ID if not set
		if entry.ID == "" {
			entry.ID = uuid.New().String()
		}

		// Set defaults
		if entry.Status == "" {
			entry.Status = domain.EmailQueueStatusPending
		}
		if entry.Priority == 0 {
			entry.Priority = domain.EmailQueuePriorityMarketing
		}
		if entry.MaxAttempts == 0 {
			entry.MaxAttempts = 3
		}

		entry.CreatedAt = now
		entry.UpdatedAt = now

		payloadJSON, err := json.Marshal(entry.Payload)
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %w", err)
		}

		insertBuilder = insertBuilder.Values(
			entry.ID, entry.Status, entry.Priority, entry.SourceType, entry.SourceID,
			entry.IntegrationID, entry.ProviderKind, entry.ContactEmail, entry.MessageID,
			entry.TemplateID, payloadJSON, entry.Attempts, entry.MaxAttempts,
			entry.CreatedAt, entry.UpdatedAt, sql.NullString{String: entry.NodeID, Valid: entry.NodeID != ""},
		)
	}

	query, args, err := insertBuilder.ToSql()
	if err != nil {
		return fmt.Errorf("failed to build query: %w", err)
	}

	_, err = tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert queue entries: %w", err)
	}

	return nil
}

// WithIntegrationQueueIdle runs fn while writes to this workspace's email queue
// are blocked. EnqueueTx takes the conflicting lock and validates the integration
// after it resumes, making check plus workspace mutation atomic for queue writers.
func (r *EmailQueueRepository) WithIntegrationQueueIdle(ctx context.Context, workspaceID, integrationID string, fn func() error) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin email integration deletion guard: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `LOCK TABLE email_queue IN SHARE MODE`); err != nil {
		return fmt.Errorf("failed to lock email queue for integration deletion: %w", err)
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM email_queue
			WHERE integration_id = $1
			  AND status IN ('pending', 'processing')
		)
	`, integrationID).Scan(&active); err != nil {
		return fmt.Errorf("failed to check active email queue entries: %w", err)
	}
	if active {
		return fmt.Errorf("%w: integration_id=%s", domain.ErrEmailIntegrationQueueActive, integrationID)
	}
	if fn == nil {
		return fmt.Errorf("integration deletion callback is required")
	}
	if err := fn(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit email integration deletion guard: %w", err)
	}
	return nil
}

// fetchPendingFairQuery lists the due entries, bounded to the `limit` best
// candidates of each (priority, source, tier) bucket, plus their tier.
//
// Veridian fork (10/10/2026, fiche 58): the historical query ordered every due
// row by (priority, created_at) and cut at LIMIT, so the oldest rows of the
// largest segment (re-planned in a loop by the caps) starved follow-ups and
// younger segments. The bucket bound keeps the candidate set small and sound;
// the final choice (follow-ups first, round robin between sources) is made by
// domain.VeridianSelectFairBatch. The due predicate is UNCHANGED.
//
// is_followup: the contact already received a mail of the SAME automation.
// The entry's own message id is excluded (a failed first attempt is not a
// previous mail), and only delivered rows (sent_at set, not failed) count.
const fetchPendingFairQuery = `
	WITH due AS (
		SELECT q.id, q.priority, q.source_id, q.created_at,
		       COALESCE(q.next_retry_at, q.created_at) AS examined_at,
		       EXISTS (
		           SELECT 1 FROM message_history mh
		           WHERE mh.contact_email = q.contact_email
		             AND mh.automation_id = q.source_id
		             AND mh.id <> q.message_id
		             AND mh.sent_at IS NOT NULL
		             AND mh.failed_at IS NULL
		       ) AS is_followup
		FROM email_queue q
		WHERE (q.status = 'pending' AND (q.next_retry_at IS NULL OR q.next_retry_at <= NOW()))
		   OR (q.status = 'failed' AND q.attempts < q.max_attempts AND q.next_retry_at <= NOW())
		   OR (q.status = 'processing' AND q.updated_at < NOW() - INTERVAL '2 minutes')
	), ranked AS (
		SELECT id, is_followup,
		       ROW_NUMBER() OVER (
		           PARTITION BY priority, source_id, is_followup
		           ORDER BY examined_at, created_at, id
		       ) AS rn
		FROM due
	)
	SELECT e.id, e.status, e.priority, e.source_type, e.source_id, e.integration_id, e.provider_kind,
	       e.contact_email, e.message_id, e.template_id, e.payload, e.attempts, e.max_attempts,
	       e.last_error, e.next_retry_at, e.created_at, e.updated_at, e.processed_at,
	       e.node_id, e.defer_reason, e.defer_detail, e.defer_profile, e.deferred_at,
	       e.defer_count, e.first_examined_at, e.last_examined_at, e.decision_logged_at,
	       r.is_followup
	FROM email_queue e
	JOIN ranked r ON r.id = e.id
	WHERE r.rn <= $1
	FOR UPDATE OF e SKIP LOCKED
`

// FetchPending retrieves pending emails for processing, fairly: explicit
// priority first, then follow-ups before first contacts, then a round robin
// between sources (automations / broadcasts), least recently examined first.
// Uses FOR UPDATE SKIP LOCKED for safe concurrent worker access.
// Includes failed emails that are ready for retry and stuck processing entries
// (>2 minutes old) for recovery after a worker crash.
func (r *EmailQueueRepository) FetchPending(ctx context.Context, workspaceID string, limit int) ([]*domain.EmailQueueEntry, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	rows, err := db.QueryContext(ctx, fetchPendingFairQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending emails: %w", err)
	}
	defer rows.Close()

	var cands []domain.VeridianFairCandidate
	for rows.Next() {
		entry, followup, err := scanEmailQueueEntryTier(rows)
		if err != nil {
			return nil, err
		}
		cands = append(cands, domain.VeridianFairCandidate{Entry: entry, IsFollowup: followup})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	rotation := r.fairRotation.Add(1)
	return domain.VeridianSelectFairBatch(cands, limit, rotation), nil
}

// MarkAsProcessing atomically marks an entry as processing
func (r *EmailQueueRepository) MarkAsProcessing(ctx context.Context, workspaceID string, id string) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	query := `
		UPDATE email_queue
		SET status = 'processing', updated_at = NOW(), attempts = attempts + 1
		WHERE id = $1 AND (
			status IN ('pending', 'failed')
			OR (status = 'processing' AND updated_at < NOW() - INTERVAL '2 minutes')
		)
	`

	result, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark email as processing: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("email not found or already processing: %s", id)
	}

	return nil
}

// MarkAsSent deletes the entry after successful send
// (entries are removed immediately rather than kept with a "sent" status)
func (r *EmailQueueRepository) MarkAsSent(ctx context.Context, workspaceID string, id string) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	query := `DELETE FROM email_queue WHERE id = $1`

	_, err = db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete sent email: %w", err)
	}

	return nil
}

// MarkAsFailed marks an entry as failed and schedules retry
func (r *EmailQueueRepository) MarkAsFailed(ctx context.Context, workspaceID string, id string, errorMsg string, nextRetryAt *time.Time) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	now := time.Now().UTC()
	query := `
		UPDATE email_queue
		SET status = 'failed', updated_at = $2, last_error = $3, next_retry_at = $4
		WHERE id = $1
	`

	_, err = db.ExecContext(ctx, query, id, now, errorMsg, nextRetryAt)
	if err != nil {
		return fmt.Errorf("failed to mark email as failed: %w", err)
	}

	return nil
}

// Delete removes a queue entry (used when max retries exhausted)
func (r *EmailQueueRepository) Delete(ctx context.Context, workspaceID string, entryID string) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	_, err = db.ExecContext(ctx, `DELETE FROM email_queue WHERE id = $1`, entryID)
	if err != nil {
		return fmt.Errorf("failed to delete queue entry: %w", err)
	}

	return nil
}

// SetNextRetry updates next_retry_at WITHOUT incrementing attempts.
func (r *EmailQueueRepository) SetNextRetry(ctx context.Context, workspaceID string, entryID string, nextRetry time.Time) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	query := `
		UPDATE email_queue
		SET next_retry_at = $1, status = 'pending', updated_at = NOW()
		WHERE id = $2
	`

	_, err = db.ExecContext(ctx, query, nextRetry, entryID)
	if err != nil {
		return fmt.Errorf("failed to set next retry: %w", err)
	}

	return nil
}

// SetDeferral reporte une entree AVEC sa raison (fiche 62), dans le MEME UPDATE :
// prochaine tentative, raison, detail, profil candidat, compteur de reports et
// horodatages d'examen. Ne ressuscite jamais une entree en pause (status 'paused'
// est conserve), et rembourse la tentative si l'entree avait ete reclamee.
func (r *EmailQueueRepository) SetDeferral(ctx context.Context, workspaceID string, entryID string, d domain.EmailQueueDeferral) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	attempts := "attempts"
	if d.RefundAttempt {
		attempts = "GREATEST(attempts - 1, 0)"
	}
	query := `
		UPDATE email_queue
		SET next_retry_at = $1,
		    status = CASE WHEN status = 'paused' THEN 'paused' ELSE 'pending' END,
		    attempts = ` + attempts + `,
		    updated_at = NOW(),
		    defer_reason = $2, defer_detail = NULLIF($3, ''), defer_profile = NULLIF($4, ''),
		    deferred_at = NOW(), defer_count = COALESCE(defer_count, 0) + 1,
		    first_examined_at = COALESCE(first_examined_at, NOW()), last_examined_at = NOW(),
		    last_error = COALESCE(NULLIF($5, ''), last_error),
		    decision_logged_at = CASE WHEN $6 THEN NOW() ELSE decision_logged_at END
		WHERE id = $7`
	if _, err := db.ExecContext(ctx, query, d.Until, d.Reason, d.Detail, d.Profile, d.LastError, d.Logged, entryID); err != nil {
		return fmt.Errorf("failed to set deferral: %w", err)
	}
	return nil
}

// WakePendingByIntegration clears deferred retries for one sending profile.
// Paused, failed and processing rows deliberately remain untouched.
func (r *EmailQueueRepository) WakePendingByIntegration(ctx context.Context, workspaceID, integrationID string) (int64, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to get database connection: %w", err)
	}
	result, err := db.ExecContext(ctx, `
		UPDATE email_queue
		SET next_retry_at = NULL, updated_at = NOW(),
		    defer_reason = NULL, defer_detail = NULL, defer_profile = NULL
		WHERE integration_id = $1
		  AND status = 'pending'
		  AND next_retry_at IS NOT NULL
	`, integrationID)
	if err != nil {
		return 0, fmt.Errorf("failed to wake pending queue entries by integration: %w", err)
	}
	return result.RowsAffected()
}

// SetNextRetryAndRefundAttempt is used only after a successful processing claim
// when the last-mile atomic quota denies SMTP.
func (r *EmailQueueRepository) SetNextRetryAndRefundAttempt(ctx context.Context, workspaceID string, entryID string, nextRetry time.Time) error {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	result, err := db.ExecContext(ctx, `
		UPDATE email_queue
		SET next_retry_at=$1, attempts=GREATEST(attempts-1, 0), status='pending', updated_at=NOW()
		WHERE id=$2 AND status='processing'
	`, nextRetry, entryID)
	if err != nil {
		return fmt.Errorf("failed to refund processing attempt: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read refunded attempt result: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("email is not processing: %s", entryID)
	}
	return nil
}

// GetStats returns queue statistics for a workspace
func (r *EmailQueueRepository) GetStats(ctx context.Context, workspaceID string) (*domain.EmailQueueStats, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	// Note: sent entries are deleted immediately, so we don't track them in stats
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0) as pending,
			COALESCE(SUM(CASE WHEN status = 'processing' THEN 1 ELSE 0 END), 0) as processing,
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) as failed
		FROM email_queue
	`

	var stats domain.EmailQueueStats
	err = db.QueryRowContext(ctx, query).Scan(
		&stats.Pending, &stats.Processing, &stats.Failed,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue stats: %w", err)
	}

	return &stats, nil
}

// GetBySourceID retrieves queue entries by source type and ID
func (r *EmailQueueRepository) GetBySourceID(ctx context.Context, workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string) ([]*domain.EmailQueueEntry, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get database connection: %w", err)
	}

	query := `
		SELECT id, status, priority, source_type, source_id, integration_id, provider_kind,
		       contact_email, message_id, template_id, payload, attempts, max_attempts,
		       last_error, next_retry_at, created_at, updated_at, processed_at
		FROM email_queue
		WHERE source_type = $1 AND source_id = $2
		ORDER BY created_at ASC
	`

	rows, err := db.QueryContext(ctx, query, sourceType, sourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query by source: %w", err)
	}
	defer rows.Close()

	var entries []*domain.EmailQueueEntry
	for rows.Next() {
		entry, err := scanEmailQueueEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

// CountBySourceAndStatus counts entries by source and status
func (r *EmailQueueRepository) CountBySourceAndStatus(ctx context.Context, workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string, status domain.EmailQueueStatus) (int64, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to get database connection: %w", err)
	}

	query := `
		SELECT COUNT(*)
		FROM email_queue
		WHERE source_type = $1 AND source_id = $2 AND status = $3
	`

	var count int64
	err = db.QueryRowContext(ctx, query, sourceType, sourceID, status).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count by source and status: %w", err)
	}

	return count, nil
}

// emailQueueExecutor is satisfied by both *sql.DB and *sql.Tx.
type emailQueueExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

const pauseBySourceSQL = `
	UPDATE email_queue
	SET status = 'paused', updated_at = NOW()
	WHERE source_type = $1 AND source_id = $2
	  AND status IN ('pending', 'failed')
`

const resumeBySourceSQL = `
	UPDATE email_queue
	SET status = 'pending', next_retry_at = NULL, updated_at = NOW(),
	    defer_reason = NULL, defer_detail = NULL, defer_profile = NULL
	WHERE source_type = $1 AND source_id = $2
	  AND status = 'paused'
`

const deleteBySourceSQL = `
	DELETE FROM email_queue
	WHERE source_type = $1 AND source_id = $2
	  AND status IN ('pending', 'failed', 'paused')
`

func execBySource(ctx context.Context, exec emailQueueExecutor, query string, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	result, err := exec.ExecContext(ctx, query, sourceType, sourceID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PauseBySource marks pending/failed entries for a source as paused.
func (r *EmailQueueRepository) PauseBySource(ctx context.Context, workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to get database connection: %w", err)
	}
	n, err := execBySource(ctx, db, pauseBySourceSQL, sourceType, sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to pause queue entries by source: %w", err)
	}
	return n, nil
}

// PauseBySourceTx is the transactional variant of PauseBySource.
func (r *EmailQueueRepository) PauseBySourceTx(ctx context.Context, tx *sql.Tx, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	n, err := execBySource(ctx, tx, pauseBySourceSQL, sourceType, sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to pause queue entries by source: %w", err)
	}
	return n, nil
}

// ResumeBySource flips paused entries back to pending and clears next_retry_at.
func (r *EmailQueueRepository) ResumeBySource(ctx context.Context, workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to get database connection: %w", err)
	}
	n, err := execBySource(ctx, db, resumeBySourceSQL, sourceType, sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to resume queue entries by source: %w", err)
	}
	return n, nil
}

// ResumeBySourceTx is the transactional variant of ResumeBySource.
func (r *EmailQueueRepository) ResumeBySourceTx(ctx context.Context, tx *sql.Tx, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	n, err := execBySource(ctx, tx, resumeBySourceSQL, sourceType, sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to resume queue entries by source: %w", err)
	}
	return n, nil
}

// DeleteBySource deletes pending/failed/paused entries for a source.
func (r *EmailQueueRepository) DeleteBySource(ctx context.Context, workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	db, err := r.getDB(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to get database connection: %w", err)
	}
	n, err := execBySource(ctx, db, deleteBySourceSQL, sourceType, sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete queue entries by source: %w", err)
	}
	return n, nil
}

// DeleteBySourceTx is the transactional variant of DeleteBySource.
func (r *EmailQueueRepository) DeleteBySourceTx(ctx context.Context, tx *sql.Tx, sourceType domain.EmailQueueSourceType, sourceID string) (int64, error) {
	n, err := execBySource(ctx, tx, deleteBySourceSQL, sourceType, sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete queue entries by source: %w", err)
	}
	return n, nil
}

// scanEmailQueueEntry scans a row into an EmailQueueEntry
// scanEmailQueueEntryTier scans the 18 queue columns followed by is_followup.
func scanEmailQueueEntryTier(rows *sql.Rows) (*domain.EmailQueueEntry, bool, error) {
	var followup bool
	var nodeID, reason, detail, profile sql.NullString
	var deferredAt, firstAt, lastAt, loggedAt sql.NullTime
	var deferCount sql.NullInt64
	entry, err := scanEmailQueueEntryWith(rows, &nodeID, &reason, &detail, &profile, &deferredAt,
		&deferCount, &firstAt, &lastAt, &loggedAt, &followup)
	if err != nil {
		return nil, false, err
	}
	entry.NodeID, entry.DeferReason, entry.DeferDetail, entry.DeferProfile =
		nodeID.String, reason.String, detail.String, profile.String
	entry.DeferCount = int(deferCount.Int64)
	if deferredAt.Valid {
		entry.DeferredAt = &deferredAt.Time
	}
	if firstAt.Valid {
		entry.FirstExaminedAt = &firstAt.Time
	}
	if lastAt.Valid {
		entry.LastExaminedAt = &lastAt.Time
	}
	if loggedAt.Valid {
		entry.DecisionLoggedAt = &loggedAt.Time
	}
	return entry, followup, nil
}

func scanEmailQueueEntry(rows *sql.Rows) (*domain.EmailQueueEntry, error) {
	return scanEmailQueueEntryWith(rows)
}

func scanEmailQueueEntryWith(rows *sql.Rows, extra ...any) (*domain.EmailQueueEntry, error) {
	var entry domain.EmailQueueEntry
	var payloadJSON []byte
	var lastError sql.NullString
	var nextRetryAt sql.NullTime
	var processedAt sql.NullTime

	dest := []any{
		&entry.ID, &entry.Status, &entry.Priority, &entry.SourceType, &entry.SourceID,
		&entry.IntegrationID, &entry.ProviderKind, &entry.ContactEmail, &entry.MessageID,
		&entry.TemplateID, &payloadJSON, &entry.Attempts, &entry.MaxAttempts,
		&lastError, &nextRetryAt, &entry.CreatedAt, &entry.UpdatedAt, &processedAt,
	}
	err := rows.Scan(append(dest, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to scan email queue entry: %w", err)
	}

	if lastError.Valid {
		entry.LastError = &lastError.String
	}
	if nextRetryAt.Valid {
		entry.NextRetryAt = &nextRetryAt.Time
	}
	if processedAt.Valid {
		entry.ProcessedAt = &processedAt.Time
	}

	if err := json.Unmarshal(payloadJSON, &entry.Payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	return &entry, nil
}
