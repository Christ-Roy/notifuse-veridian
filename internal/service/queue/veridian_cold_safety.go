package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

const (
	coldSafetyRetryDelay         = 5 * time.Minute
	coldSafetySuppressionTimeout = 10 * time.Second
	coldSafetyQuotaTimeout       = 3 * time.Second
)

// ColdSafetyDecision is evaluated after the queue row is claimed and before
// either the v55 quota ledger or the email transport can be reached.
type ColdSafetyDecision struct {
	Allowed bool
	Reason  string
	RetryAt time.Time
}

// ColdSafetyLease identifies a reservation created by this authorization.
// created=false means an earlier attempt already owns the idempotency key and
// must not be deleted by the current worker.
type ColdSafetyLease struct {
	workspaceID  string
	queueEntryID string
	attempt      int
	quotaDate    string
	created      bool
}

type ColdSafetyGuard struct {
	workspaceRepo domain.WorkspaceRepository
	now           func() time.Time
}

func NewColdSafetyGuard(workspaceRepo domain.WorkspaceRepository) *ColdSafetyGuard {
	return &ColdSafetyGuard{
		workspaceRepo: workspaceRepo,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

func emailSHA256(email string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(digest[:])
}

// Authorize fails closed on incomplete policy, unavailable suppression data or
// unavailable quota state. A successful decision returns a compensatable lease.
func (g *ColdSafetyGuard) Authorize(
	ctx context.Context,
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
) (ColdSafetyDecision, *ColdSafetyLease) {
	settings := &workspace.Settings
	if !settings.VeridianColdSafetyEnabled {
		return ColdSafetyDecision{Allowed: true, Reason: "cold_safety_disabled"}, nil
	}

	now := g.now().UTC()
	deny := func(reason string, retryAt time.Time) (ColdSafetyDecision, *ColdSafetyLease) {
		if retryAt.IsZero() {
			retryAt = now.Add(coldSafetyRetryDelay)
		}
		return ColdSafetyDecision{Allowed: false, Reason: reason, RetryAt: retryAt}, nil
	}
	if err := settings.ValidateVeridianColdSafety(); err != nil {
		return deny("invalid_policy:"+err.Error(), time.Time{})
	}
	if strings.TrimSpace(entry.ID) == "" {
		return deny("missing_queue_entry_id", time.Time{})
	}
	if strings.TrimSpace(entry.MessageID) == "" {
		return deny("missing_message_id", time.Time{})
	}

	recipient := strings.ToLower(strings.TrimSpace(entry.ContactEmail))
	at := strings.LastIndexByte(recipient, '@')
	if at <= 0 || at == len(recipient)-1 {
		return deny("invalid_recipient", time.Time{})
	}
	recipientDomain := recipient[at+1:]
	sender := strings.ToLower(strings.TrimSpace(entry.Payload.FromAddress))
	if sender == "" {
		return deny("missing_sender", time.Time{})
	}
	providerClass := strings.ToLower(strings.TrimSpace(entry.Payload.VeridianProviderClass))
	if providerClass == "" || !domain.IsValidProviderClass(providerClass) {
		return deny("invalid_provider_class:"+providerClass, time.Time{})
	}
	for _, excluded := range settings.VeridianExcludedProviderClasses {
		if excluded == providerClass {
			return deny("excluded_provider_class:"+providerClass, nextColdSafetyQuotaDay(now))
		}
	}
	providerCap, capOK := settings.VeridianProviderClassDailyCap[providerClass]
	providerRate, rateOK := settings.VeridianProviderClassRates[providerClass]
	if !capOK || providerCap <= 0 || !rateOK || providerRate <= 0 {
		return deny("unconfigured_provider_class:"+providerClass, time.Time{})
	}

	suppressed, reason, err := g.globallySuppressed(ctx, recipient, entry.ContactEmail)
	if err != nil {
		return deny("global_suppression_unavailable:"+err.Error(), time.Time{})
	}
	if suppressed {
		return deny("globally_suppressed:"+reason, nextColdSafetyQuotaDay(now))
	}

	decision, lease, err := g.reserveQuota(
		ctx,
		workspace.ID,
		entry.ID,
		entry.MessageID,
		entry.Attempts+1,
		sender,
		providerClass,
		recipientDomain,
		emailSHA256(recipient),
		providerRate,
		providerCap,
		settings,
	)
	if err != nil {
		return deny("quota_unavailable:"+err.Error(), time.Time{})
	}
	return decision, lease
}

func (g *ColdSafetyGuard) globallySuppressed(ctx context.Context, recipient, originalEmail string) (bool, string, error) {
	guardCtx, cancel := context.WithTimeout(ctx, coldSafetySuppressionTimeout)
	defer cancel()

	systemDB, err := g.workspaceRepo.GetSystemConnection(guardCtx)
	if err != nil {
		return false, "", fmt.Errorf("system_db:%w", err)
	}
	emailHash := emailSHA256(recipient)
	var persistedStatus string
	err = systemDB.QueryRowContext(guardCtx,
		`SELECT status FROM veridian_global_suppressions WHERE email_sha256 = $1`,
		emailHash,
	).Scan(&persistedStatus)
	if err == nil {
		return true, persistedStatus, nil
	}
	if err != sql.ErrNoRows {
		return false, "", fmt.Errorf("global_lookup:%w", err)
	}

	workspaces, err := g.workspaceRepo.List(guardCtx)
	if err != nil {
		return false, "", fmt.Errorf("workspace_inventory:%w", err)
	}
	if len(workspaces) == 0 {
		return false, "", fmt.Errorf("workspace_inventory_empty")
	}
	for _, workspace := range workspaces {
		workspaceDB, err := g.workspaceRepo.GetConnection(guardCtx, workspace.ID)
		if err != nil {
			return false, "", fmt.Errorf("workspace_connection:%s:%w", workspace.ID, err)
		}
		var status string
		err = workspaceDB.QueryRowContext(guardCtx, `
			SELECT status
			FROM (
				SELECT status::text AS status
				FROM contact_lists
				WHERE (email = $1 OR email = $2)
				  AND status IN ('unsubscribed', 'bounced', 'complained')
				UNION ALL
				SELECT CASE
					WHEN complained_at IS NOT NULL THEN 'complained'
					WHEN bounced_at IS NOT NULL THEN 'bounced'
					ELSE 'unsubscribed'
				END AS status
				FROM message_history
				WHERE (contact_email = $1 OR contact_email = $2)
				  AND (complained_at IS NOT NULL OR bounced_at IS NOT NULL OR unsubscribed_at IS NOT NULL)
			) AS terminal_suppressions
			ORDER BY CASE status WHEN 'complained' THEN 1 WHEN 'bounced' THEN 2 ELSE 3 END
			LIMIT 1`, recipient, originalEmail).Scan(&status)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return false, "", fmt.Errorf("workspace_suppression:%s:%w", workspace.ID, err)
		}
		if _, err := systemDB.ExecContext(guardCtx, `
			INSERT INTO veridian_global_suppressions
				(email_sha256, status, source_workspace_id, first_observed_at, last_observed_at)
			VALUES ($1, $2, $3, NOW(), NOW())
			ON CONFLICT (email_sha256) DO UPDATE SET
				status = CASE
					WHEN veridian_global_suppressions.status = 'complained' OR EXCLUDED.status = 'complained' THEN 'complained'
					WHEN veridian_global_suppressions.status = 'bounced' OR EXCLUDED.status = 'bounced' THEN 'bounced'
					ELSE 'unsubscribed'
				END,
				source_workspace_id = EXCLUDED.source_workspace_id,
				last_observed_at = NOW()`, emailHash, status, workspace.ID); err != nil {
			return false, "", fmt.Errorf("global_persist:%w", err)
		}
		return true, status, nil
	}
	return false, "", nil
}

func nextColdSafetyQuotaDay(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

func (g *ColdSafetyGuard) reserveQuota(
	ctx context.Context,
	workspaceID string,
	queueEntryID string,
	messageID string,
	attempt int,
	sender string,
	providerClass string,
	recipientDomain string,
	recipientHash string,
	providerRate float64,
	providerCap int,
	settings *domain.WorkspaceSettings,
) (ColdSafetyDecision, *ColdSafetyLease, error) {
	quotaCtx, cancel := context.WithTimeout(ctx, coldSafetyQuotaTimeout)
	defer cancel()

	systemDB, err := g.workspaceRepo.GetSystemConnection(quotaCtx)
	if err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("system_db:%w", err)
	}
	now := g.now().UTC()
	quotaDate := now.Format("2006-01-02")
	nextDay := nextColdSafetyQuotaDay(now)

	tx, err := systemDB.BeginTx(quotaCtx, nil)
	if err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("begin:%w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(quotaCtx, `SELECT pg_advisory_xact_lock(hashtext($1))`, workspaceID+":"+quotaDate); err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("lock:%w", err)
	}

	var exists bool
	if err := tx.QueryRowContext(quotaCtx, `
		SELECT EXISTS(
			SELECT 1 FROM veridian_send_reservations
			WHERE workspace_id = $1 AND queue_entry_id = $2 AND attempt = $3 AND quota_date = $4
		)`, workspaceID, queueEntryID, attempt, quotaDate).Scan(&exists); err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("idempotency:%w", err)
	}
	if exists {
		if err := tx.Commit(); err != nil {
			return ColdSafetyDecision{}, nil, fmt.Errorf("commit_idempotent:%w", err)
		}
		return ColdSafetyDecision{Allowed: true, Reason: "quota_already_reserved"}, &ColdSafetyLease{
			workspaceID:  workspaceID,
			queueEntryID: queueEntryID,
			attempt:      attempt,
			quotaDate:    quotaDate,
		}, nil
	}

	var workspaceCount, senderCount, providerCount, domainCount, recipientCount int
	var lastProviderReservation sql.NullTime
	if err := tx.QueryRowContext(quotaCtx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE sender_email = $3),
			COUNT(*) FILTER (WHERE provider_class = $4),
			COUNT(*) FILTER (WHERE recipient_domain = $5),
			COUNT(*) FILTER (WHERE recipient_sha256 = $6),
			(SELECT MAX(previous.reserved_at)
			 FROM veridian_send_reservations AS previous
			 WHERE previous.workspace_id = $1
			   AND previous.provider_class = $4)
		FROM veridian_send_reservations
		WHERE workspace_id = $1 AND quota_date = $2`,
		workspaceID, quotaDate, sender, providerClass, recipientDomain, recipientHash,
	).Scan(
		&workspaceCount,
		&senderCount,
		&providerCount,
		&domainCount,
		&recipientCount,
		&lastProviderReservation,
	); err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("usage:%w", err)
	}

	limits := []struct {
		name  string
		used  int
		limit int
	}{
		{"workspace_daily", workspaceCount, settings.VeridianWorkspaceDailyCap},
		{"sender_daily", senderCount, settings.VeridianPerSenderDailyCap},
		{"provider_daily:" + providerClass, providerCount, providerCap},
		{"recipient_domain_daily:" + recipientDomain, domainCount, settings.VeridianRecipientDomainDailyCap},
		{"recipient_daily", recipientCount, settings.VeridianPerRecipientDailyCap},
	}
	for _, limit := range limits {
		if limit.used >= limit.limit {
			return ColdSafetyDecision{
				Allowed: false,
				Reason:  "cap_reached:" + limit.name,
				RetryAt: nextDay,
			}, nil, nil
		}
	}

	minimumGap := time.Duration(float64(time.Minute) / providerRate)
	if lastProviderReservation.Valid {
		retryAt := lastProviderReservation.Time.Add(minimumGap)
		if now.Before(retryAt) {
			return ColdSafetyDecision{
				Allowed: false,
				Reason:  "provider_rate:" + providerClass,
				RetryAt: retryAt,
			}, nil, nil
		}
	}

	if _, err := tx.ExecContext(quotaCtx, `
		INSERT INTO veridian_send_reservations
			(workspace_id, queue_entry_id, message_id, attempt, sender_email, provider_class,
			 recipient_domain, recipient_sha256, quota_date, reserved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		workspaceID,
		queueEntryID,
		messageID,
		attempt,
		sender,
		providerClass,
		recipientDomain,
		recipientHash,
		quotaDate,
		now,
	); err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("reserve:%w", err)
	}
	if err := tx.Commit(); err != nil {
		return ColdSafetyDecision{}, nil, fmt.Errorf("commit:%w", err)
	}
	return ColdSafetyDecision{Allowed: true, Reason: "quota_reserved"}, &ColdSafetyLease{
		workspaceID:  workspaceID,
		queueEntryID: queueEntryID,
		attempt:      attempt,
		quotaDate:    quotaDate,
		created:      true,
	}, nil
}

// Release compensates only a reservation created by the current authorization.
// It is used when a later local gate proves no provider attempt can occur.
func (g *ColdSafetyGuard) Release(ctx context.Context, lease *ColdSafetyLease) error {
	if lease == nil || !lease.created {
		return nil
	}
	releaseCtx, cancel := context.WithTimeout(ctx, coldSafetyQuotaTimeout)
	defer cancel()

	systemDB, err := g.workspaceRepo.GetSystemConnection(releaseCtx)
	if err != nil {
		return fmt.Errorf("system_db:%w", err)
	}
	if _, err := systemDB.ExecContext(releaseCtx, `
		DELETE FROM veridian_send_reservations
		WHERE workspace_id = $1 AND queue_entry_id = $2 AND attempt = $3 AND quota_date = $4`,
		lease.workspaceID, lease.queueEntryID, lease.attempt, lease.quotaDate); err != nil {
		return fmt.Errorf("release:%w", err)
	}
	return nil
}

func (w *EmailQueueWorker) veridianReleaseColdSafetyLease(lease *ColdSafetyLease, entryID string) {
	if w.coldSafetyGuard == nil || lease == nil {
		return
	}
	if err := w.coldSafetyGuard.Release(w.ctx, lease); err != nil {
		// A failed compensation remains conservative: the reservation stays
		// consumed instead of risking an over-send on the retry.
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entryID,
			"error":    err.Error(),
		}).Error("Failed to release cold safety reservation")
	}
}
