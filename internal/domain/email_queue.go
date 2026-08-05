package domain

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

//go:generate mockgen -destination mocks/mock_email_queue_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain EmailQueueRepository

// EmailQueueStatus represents the status of a queued email
type EmailQueueStatus string

const (
	EmailQueueStatusPending    EmailQueueStatus = "pending"
	EmailQueueStatusProcessing EmailQueueStatus = "processing"
	EmailQueueStatusFailed     EmailQueueStatus = "failed"
	EmailQueueStatusPaused     EmailQueueStatus = "paused"
	// Note: There is no "sent" status - entries are deleted immediately after successful send
)

// ErrEmailIntegrationQueueActive prevents deleting an email integration while
// queued work still owns its exact transport identity.
var ErrEmailIntegrationQueueActive = errors.New("email integration has pending or processing queue entries")

// EmailQueueSourceType identifies the origin of the queued email
type EmailQueueSourceType string

const (
	EmailQueueSourceBroadcast  EmailQueueSourceType = "broadcast"
	EmailQueueSourceAutomation EmailQueueSourceType = "automation"
)

// Default priority for marketing emails (broadcasts and automations)
const EmailQueuePriorityMarketing = 5

// EmailQueueEntry represents a single email in the queue
type EmailQueueEntry struct {
	ID            string               `json:"id"`
	Status        EmailQueueStatus     `json:"status"`
	Priority      int                  `json:"priority"`
	SourceType    EmailQueueSourceType `json:"source_type"`
	SourceID      string               `json:"source_id"` // BroadcastID or AutomationID
	IntegrationID string               `json:"integration_id"`
	ProviderKind  EmailProviderKind    `json:"provider_kind"`

	// Email identification
	ContactEmail string `json:"contact_email"`
	MessageID    string `json:"message_id"`
	TemplateID   string `json:"template_id"`

	// Serialized payload for sending (contains all data needed to send)
	Payload EmailQueuePayload `json:"payload"`

	// Retry tracking
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	LastError   *string    `json:"last_error,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// EmailQueuePayload contains all data needed to send the email
// This is stored as JSONB in the database
type EmailQueuePayload struct {
	// Email content (compiled and ready to send)
	FromAddress   string `json:"from_address"`
	FromName      string `json:"from_name"`
	Subject       string `json:"subject"`
	HTMLContent   string `json:"html_content"`
	TextContent   string `json:"text_content,omitempty"`
	PlainTextOnly bool   `json:"plain_text_only,omitempty"`

	// Options
	EmailOptions EmailOptions `json:"email_options"`

	// Provider configuration (rate limit needed for worker)
	RateLimitPerMinute int `json:"rate_limit_per_minute"`

	// Provider settings (encrypted, will be decrypted by worker)
	ProviderSettings map[string]interface{} `json:"provider_settings"`

	// Message history tracking fields
	TemplateVersion int                    `json:"template_version"`        // Needed for message_history
	ListID          string                 `json:"list_id,omitempty"`       // For broadcasts
	TemplateData    map[string]interface{} `json:"template_data,omitempty"` // For message history logging

	// Veridian fork — throttle par classe de provider destinataire (cold
	// outbound). Champs optionnels posés à l'enqueue, consommés par le worker.
	// Vides = comportement upstream inchangé. Cf. domain/veridian_provider_class.go.
	VeridianProviderClass      string             `json:"veridian_provider_class,omitempty"`
	VeridianProviderClassRates map[string]float64 `json:"veridian_provider_class_rates,omitempty"`

	// Veridian fork — plafonds JOURNALIERS (cold outbound). Distincts du débit
	// par minute ci-dessus : ce sont des volumes maximaux par jour calendaire
	// (source de vérité = COUNT message_history depuis minuit, cf.
	// veridian_daily_cap.go). Copiés à l'enqueue depuis broadcast.metadata ;
	// fallback workspace lu en live par le worker. 0/vide = pas de plafond.
	VeridianProviderClassDailyCap map[string]int `json:"veridian_provider_class_daily_cap,omitempty"`
	VeridianPerRecipientDailyCap  int            `json:"veridian_per_recipient_daily_cap,omitempty"`

	// Veridian fork — plafond JOURNALIER par ADRESSE ÉMETTRICE (warmup IP). Niveau
	// le plus spécifique de la cascade (BROADCAST → infra → workspace). Dimension
	// ÉMETTRICE (≠ caps destinataire ci-dessus) : max N envois/jour par boîte
	// d'envoi. Copié à l'enqueue depuis broadcast.metadata ; 0 = pas de plafond.
	VeridianPerSenderDailyCap int `json:"veridian_per_sender_daily_cap,omitempty"`

	// Veridian fork — FENÊTRE D'ENVOI (cold outbound) copiée à l'enqueue depuis
	// broadcast.metadata. Niveau le plus spécifique de la cascade ; le worker
	// retombe sur l'infra puis le workspace si nil. nil = pas de fenêtre sur ce
	// broadcast (héritage). Cf. veridian_sending_window.go.
	VeridianSendingWindow *VeridianSendingWindow `json:"veridian_sending_window,omitempty"`

	// Veridian fork — JITTER TEMPOREL (cold outbound) copié à l'enqueue depuis
	// broadcast.metadata. Amplitude (±) de dispersion du délai de re-planification
	// du throttle minute, en fraction du pas nominal. Niveau le plus spécifique
	// de la cascade (broadcast → infra → workspace). nil = non configuré (le gate
	// applique le défaut cold), *0 = jitter désactivé. Cf. veridian_jitter.go.
	VeridianJitterPct *float64 `json:"veridian_jitter_pct,omitempty"`

	// Veridian fork — ANTI-HASH IDENTIQUE (cold outbound). Hash du rendu final
	// normalisé (sujet + corps), posé à l'enqueue APRÈS spintax + déduplication.
	// Persisté dans message_history (colonne veridian_content_hash) pour la
	// fenêtre glissante anti-collision par classe ; relu par le filet best-effort
	// du worker (veridian_content_hash_gate.go). Vide = pas calculé (hors contexte
	// cold / anti-hash désactivé). Cf. veridian_content_hash.go.
	VeridianContentHash string `json:"veridian_content_hash,omitempty"`

	// Veridian fork — EXCLUSION de classes de provider destinataire (cold
	// outbound). Liste de classes à NE PAS contacter (ex. ["microsoft"] pour
	// épargner une IP en warm-up). Distinct des rates/caps (0 ≠ exclu) : levier
	// DÉDIÉ. Copié à l'enqueue depuis broadcast.metadata ; fallback infra puis
	// workspace lu en live par le gate worker (veridianExcludedClassGate, qui
	// route un destinataire exclu en échec PERMANENT sans ouvrir de SMTP). Vide =
	// aucune exclusion (non-régression). Cf. veridian_excluded_classes.go.
	VeridianExcludedProviderClasses []string `json:"veridian_excluded_provider_classes,omitempty"`
}

// ToSendEmailProviderRequest converts the payload to a SendEmailProviderRequest
// The provider must be passed in separately as it's not stored in the payload
func (p *EmailQueuePayload) ToSendEmailProviderRequest(workspaceID, integrationID, messageID, toEmail string, provider *EmailProvider) *SendEmailProviderRequest {
	return &SendEmailProviderRequest{
		WorkspaceID:   workspaceID,
		IntegrationID: integrationID,
		MessageID:     messageID,
		FromAddress:   p.FromAddress,
		FromName:      p.FromName,
		To:            toEmail,
		Subject:       p.Subject,
		Content:       p.HTMLContent,
		TextContent:   p.TextContent,
		PlainTextOnly: p.PlainTextOnly,
		Provider:      provider,
		EmailOptions:  p.EmailOptions,
	}
}

// EmailQueueStats provides queue statistics for a workspace
type EmailQueueStats struct {
	Pending    int64 `json:"pending"`
	Processing int64 `json:"processing"`
	Failed     int64 `json:"failed"`
	// Note: Sent entries are deleted immediately, not tracked in stats
}

// EmailQueueRepository defines data access for the email queue
type EmailQueueRepository interface {
	EmailIntegrationLifecycleRepository

	// Enqueue adds emails to the queue
	Enqueue(ctx context.Context, workspaceID string, entries []*EmailQueueEntry) error

	// EnqueueTx adds emails to the queue within an existing transaction
	EnqueueTx(ctx context.Context, tx *sql.Tx, workspaceID string, entries []*EmailQueueEntry) error

	// FetchPending retrieves pending emails for processing
	// Uses FOR UPDATE SKIP LOCKED to allow concurrent workers
	// Orders by priority ASC (lower = higher priority), then created_at ASC
	FetchPending(ctx context.Context, workspaceID string, limit int) ([]*EmailQueueEntry, error)

	// MarkAsProcessing atomically marks an entry as processing
	MarkAsProcessing(ctx context.Context, workspaceID string, id string) error

	// MarkAsSent deletes the entry after successful send
	// (entries are removed immediately rather than marked with a "sent" status)
	MarkAsSent(ctx context.Context, workspaceID string, id string) error

	// MarkAsFailed marks an entry as failed and schedules retry
	MarkAsFailed(ctx context.Context, workspaceID string, id string, errorMsg string, nextRetryAt *time.Time) error

	// Delete removes a queue entry (used when max retries exhausted)
	Delete(ctx context.Context, workspaceID string, entryID string) error

	// SetNextRetry updates next_retry_at WITHOUT incrementing attempts.
	// Used by circuit breaker to schedule retry without burning retry attempts
	SetNextRetry(ctx context.Context, workspaceID string, entryID string, nextRetry time.Time) error

	// SetNextRetryAndRefundAttempt returns a claimed processing row to pending and
	// atomically refunds the attempt increment made by MarkAsProcessing.
	SetNextRetryAndRefundAttempt(ctx context.Context, workspaceID string, entryID string, nextRetry time.Time) error

	// GetStats returns queue statistics for a workspace
	GetStats(ctx context.Context, workspaceID string) (*EmailQueueStats, error)

	// GetBySourceID retrieves queue entries by source type and ID
	// Useful for tracking broadcast/automation progress
	GetBySourceID(ctx context.Context, workspaceID string, sourceType EmailQueueSourceType, sourceID string) ([]*EmailQueueEntry, error)

	// CountBySourceAndStatus counts entries by source and status
	CountBySourceAndStatus(ctx context.Context, workspaceID string, sourceType EmailQueueSourceType, sourceID string, status EmailQueueStatus) (int64, error)

	// PauseBySource marks all pending/failed entries for a source as paused.
	// Processing entries are untouched (mid-send, will complete naturally).
	// Returns the number of rows affected.
	PauseBySource(ctx context.Context, workspaceID string, sourceType EmailQueueSourceType, sourceID string) (int64, error)

	// PauseBySourceTx is the transactional variant of PauseBySource.
	PauseBySourceTx(ctx context.Context, tx *sql.Tx, sourceType EmailQueueSourceType, sourceID string) (int64, error)

	// ResumeBySource marks all paused entries for a source back to pending
	// and clears next_retry_at so retries pick up immediately.
	// Returns the number of rows affected.
	ResumeBySource(ctx context.Context, workspaceID string, sourceType EmailQueueSourceType, sourceID string) (int64, error)

	// ResumeBySourceTx is the transactional variant of ResumeBySource.
	ResumeBySourceTx(ctx context.Context, tx *sql.Tx, sourceType EmailQueueSourceType, sourceID string) (int64, error)

	// DeleteBySource deletes all pending/failed/paused entries for a source.
	// Processing entries are untouched (mid-send, will complete naturally).
	// Returns the number of rows deleted.
	DeleteBySource(ctx context.Context, workspaceID string, sourceType EmailQueueSourceType, sourceID string) (int64, error)

	// DeleteBySourceTx is the transactional variant of DeleteBySource.
	DeleteBySourceTx(ctx context.Context, tx *sql.Tx, sourceType EmailQueueSourceType, sourceID string) (int64, error)
}

// EmailIntegrationLifecycleRepository serializes integration deletion against
// queue inserts in the workspace database.
type EmailIntegrationLifecycleRepository interface {
	WithIntegrationQueueIdle(ctx context.Context, workspaceID, integrationID string, fn func() error) error
}

// EmailIntegrationPolicyQueueRepository wakes pending rows so a profile policy
// edit (window, rate or cap) is re-evaluated immediately by the worker.
type EmailIntegrationPolicyQueueRepository interface {
	WakePendingByIntegration(ctx context.Context, workspaceID, integrationID string) (int64, error)
}

// getEmailQueueRetryBase returns the base retry interval for exponential backoff.
// Can be overridden via EMAIL_QUEUE_RETRY_BASE environment variable for testing.
// Default is 1 minute.
func getEmailQueueRetryBase() time.Duration {
	if base := os.Getenv("EMAIL_QUEUE_RETRY_BASE"); base != "" {
		if d, err := time.ParseDuration(base); err == nil {
			return d
		}
	}
	return 1 * time.Minute
}

// CalculateNextRetryTime calculates the next retry time using exponential backoff
// Backoff: base, 2*base, 4*base for attempts 1, 2, 3 (default base = 1min)
func CalculateNextRetryTime(attempts int) time.Time {
	if attempts <= 0 {
		attempts = 1
	}
	// 2^(attempts-1) * base
	base := getEmailQueueRetryBase()
	multiplier := 1 << uint(attempts-1)
	return time.Now().UTC().Add(time.Duration(multiplier) * base)
}
