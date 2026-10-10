package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/emailerror"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// EmailQueueWorkerConfig holds configuration for the worker pool
type EmailQueueWorkerConfig struct {
	WorkerCount  int           // Number of concurrent workers per workspace (default: 5)
	PollInterval time.Duration // How often to poll for new work (default: 1s)
	BatchSize    int           // How many emails to fetch per poll (default: 50)
	MaxRetries   int           // Max retry attempts before permanent failure (default: 3)

	// Circuit breaker settings
	CircuitBreakerThreshold int           // Provider errors before opening circuit (default: 5)
	CircuitBreakerCooldown  time.Duration // Time before auto-reset attempt (default: 1 minute)
}

// DefaultWorkerConfig returns sensible default configuration
func DefaultWorkerConfig() *EmailQueueWorkerConfig {
	return &EmailQueueWorkerConfig{
		WorkerCount:             5,
		PollInterval:            1 * time.Second,
		BatchSize:               50,
		MaxRetries:              3,
		CircuitBreakerThreshold: 5,
		CircuitBreakerCooldown:  getCircuitBreakerCooldown(),
	}
}

// EmailSentCallback is called when an email is successfully sent.
// Veridian fix 2026-09-29: contactEmail added so a caller (the automation
// executor) can resolve which parked contact_automation to advance, without a
// second lookup keyed only on messageID.
type EmailSentCallback func(workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string, contactEmail string, messageID string)

// EmailFailedCallback is called when an email fails to send.
// Veridian fix 2026-09-29: contactEmail added, same reason as EmailSentCallback.
type EmailFailedCallback func(workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string, contactEmail string, messageID string, err error, isPermanent bool)

// EmailQueueWorker processes queued emails
type EmailQueueWorker struct {
	queueRepo          domain.EmailQueueRepository
	workspaceRepo      domain.WorkspaceRepository
	emailService       domain.EmailServiceInterface
	messageHistoryRepo domain.MessageHistoryRepository
	rateLimiter        *IntegrationRateLimiter
	// Veridian fork: second rate-limiting stage, keyed by recipient provider
	// class (cf. veridian_provider_throttle.go). No-op without configuration.
	providerClassLimiter *ProviderClassRateLimiter
	// Veridian fork (2026-10-07): facteur de ralentissement par couple (domaine
	// émetteur, classe), posé par le fusible de réputation et lu par les gates de
	// débit et de plafond (cf. veridian_reputation_gate.go).
	reputationFactors veridianReputationFactors
	// Veridian fork (Lot 4): classifies the recipient provider by REAL MX
	// (cf. domain.VeridianMXClassifier). Suffix-known domains resolve with zero
	// I/O; unknown domains do a cached MX lookup (best-effort, short timeout).
	// Never nil after the constructor; replaceable in tests via the setter.
	providerMXClassifier *domain.VeridianMXClassifier
	dailyQuotaBackfilled sync.Map
	circuitBreaker       *IntegrationCircuitBreaker
	errorClassifier      *emailerror.Classifier
	config               *EmailQueueWorkerConfig
	logger               logger.Logger

	// Final automation gate dependencies. The executor checks these before
	// enqueue, but a reply/unsubscribe/pause can happen while a row is waiting.
	automationRepo   domain.AutomationRepository
	contactListRepo  domain.ContactListRepository
	contactReplyRepo domain.VeridianContactReplyRepository
	// The upstream-compatible constructor is used directly by many unit tests.
	// Production explicitly enables the last-mile guards through the setter.
	finalSendGuardsConfigured bool

	// Veridian fork: re-rend le contenu des entrees d'automation au depilage
	// (cf. veridian_render_at_send.go). nil = comportement upstream.
	queuedEmailRenderer QueuedEmailRenderer

	// Veridian fork (fiche 62) : journal des decisions d'envoi. nil = pas de journal
	// (la raison de report reste posee sur l'entree de file).
	decisionLog domain.VeridianSendDecisionRepository

	// Control
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
	mu      sync.RWMutex

	// Callbacks for progress tracking
	onEmailSent   EmailSentCallback
	onEmailFailed EmailFailedCallback
}

// SetAutomationSendGuard wires the durable, last-mile stop checks used only for
// automation queue entries. It is kept as a setter to preserve the upstream
// worker constructor surface; production wires it immediately after creation.
func (w *EmailQueueWorker) SetAutomationSendGuard(
	automationRepo domain.AutomationRepository,
	contactListRepo domain.ContactListRepository,
	contactReplyRepo domain.VeridianContactReplyRepository,
) {
	w.automationRepo = automationRepo
	w.contactListRepo = contactListRepo
	w.contactReplyRepo = contactReplyRepo
	w.finalSendGuardsConfigured = true
}

// NewEmailQueueWorker creates a new EmailQueueWorker
func NewEmailQueueWorker(
	queueRepo domain.EmailQueueRepository,
	workspaceRepo domain.WorkspaceRepository,
	emailService domain.EmailServiceInterface,
	messageHistoryRepo domain.MessageHistoryRepository,
	config *EmailQueueWorkerConfig,
	log logger.Logger,
) *EmailQueueWorker {
	if config == nil {
		config = DefaultWorkerConfig()
	}

	// Setup circuit breaker config with defaults
	cbConfig := CircuitBreakerConfig{
		Threshold:      config.CircuitBreakerThreshold,
		CooldownPeriod: config.CircuitBreakerCooldown,
	}
	if cbConfig.Threshold == 0 {
		cbConfig.Threshold = 5
	}
	if cbConfig.CooldownPeriod == 0 {
		cbConfig.CooldownPeriod = getCircuitBreakerCooldown()
	}

	return &EmailQueueWorker{
		queueRepo:            queueRepo,
		workspaceRepo:        workspaceRepo,
		emailService:         emailService,
		messageHistoryRepo:   messageHistoryRepo,
		rateLimiter:          NewIntegrationRateLimiter(),
		providerClassLimiter: NewProviderClassRateLimiter(),
		providerMXClassifier: domain.NewVeridianMXClassifier(nil), // default net resolver (8.8.8.8 / 1.1.1.1)
		circuitBreaker:       NewIntegrationCircuitBreaker(cbConfig),
		errorClassifier:      emailerror.NewClassifier(),
		config:               config,
		logger:               log,
	}
}

// SetCallbacks sets callback functions for progress tracking
func (w *EmailQueueWorker) SetCallbacks(onSent EmailSentCallback, onFailed EmailFailedCallback) {
	w.onEmailSent = onSent
	w.onEmailFailed = onFailed
}

// Start begins processing queued emails
func (w *EmailQueueWorker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.ctx, w.cancel = context.WithCancel(ctx)
	w.running = true
	w.mu.Unlock()

	w.logger.WithFields(map[string]interface{}{
		"worker_count":  w.config.WorkerCount,
		"poll_interval": w.config.PollInterval.String(),
		"batch_size":    w.config.BatchSize,
	}).Info("Starting email queue worker")

	// Start the main processing loop
	w.wg.Add(1)
	go w.processLoop()

	return nil
}

// Stop gracefully stops all workers
func (w *EmailQueueWorker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	w.cancel()
	w.mu.Unlock()

	w.logger.Info("Stopping email queue worker...")
	w.wg.Wait()
	w.logger.Info("Email queue worker stopped")
}

// IsRunning returns whether the worker is currently running
func (w *EmailQueueWorker) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// processLoop is the main processing loop that polls for work
func (w *EmailQueueWorker) processLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.processAllWorkspaces()
		}
	}
}

// processAllWorkspaces processes pending emails from all workspaces
func (w *EmailQueueWorker) processAllWorkspaces() {
	// Get list of all workspaces
	workspaces, err := w.workspaceRepo.List(w.ctx)
	if err != nil {
		w.logger.WithField("error", err.Error()).Error("Failed to list workspaces")
		return
	}

	// Process each workspace concurrently
	var processWg sync.WaitGroup
	semaphore := make(chan struct{}, w.config.WorkerCount)

	for _, workspace := range workspaces {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		semaphore <- struct{}{}
		processWg.Add(1)

		go func(ws *domain.Workspace) {
			defer processWg.Done()
			defer func() { <-semaphore }()

			w.processWorkspace(ws)
		}(workspace)
	}

	processWg.Wait()
}

// processWorkspace processes pending emails for a single workspace
func (w *EmailQueueWorker) processWorkspace(workspace *domain.Workspace) {
	// Calculate dynamic batch size based on rate limit
	// Use 45 seconds as time budget (leave 15s buffer for shutdown)
	minRate := w.getMinEmailRateLimit(workspace)
	effectiveBatchSize := (minRate * 45) / 60 // 75% of what we can send in 1 minute
	if effectiveBatchSize < 1 {
		effectiveBatchSize = 1
	}
	if effectiveBatchSize > w.config.BatchSize {
		effectiveBatchSize = w.config.BatchSize
	}

	// Fetch pending emails
	entries, err := w.queueRepo.FetchPending(w.ctx, workspace.ID, effectiveBatchSize)
	if err != nil {
		w.logger.WithFields(map[string]interface{}{
			"workspace_id": workspace.ID,
			"error":        err.Error(),
		}).Error("Failed to fetch pending emails")
		return
	}

	if len(entries) == 0 {
		return
	}

	w.logger.WithFields(map[string]interface{}{
		"workspace_id": workspace.ID,
		"count":        len(entries),
	}).Debug("Processing queued emails")

	// Process each entry
	for _, entry := range entries {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		w.processEntry(workspace, entry)
	}
}

// processEntry processes a single queue entry
func (w *EmailQueueWorker) processEntry(workspace *domain.Workspace, entry *domain.EmailQueueEntry) {
	// Veridian fork (lot 4, 08/10/2026) : un mail transactionnel de sequence part par
	// le seul profil transactionnel reserve, sans aucune porte commerciale. Sans
	// profil reserve, l'entree suit le chemin commercial ci-dessous, comme avant.
	// Cf. veridian_transactional_entry.go.
	if w.veridianProcessTransactionalEntry(workspace, entry) {
		return
	}
	// Get the ASSIGNED integration (resolved at enqueue). It may be replaced
	// below by a sibling from the rotation pool if it has no room — cf. the
	// pool failover block right after recipient classification.
	integration := workspace.GetIntegrationByID(entry.IntegrationID)
	if integration == nil {
		// Mark as processing first to increment attempts, then handle error
		if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"error":    err.Error(),
			}).Warn("Failed to mark entry as processing")
			return
		}
		w.handleError(workspace, entry, fmt.Errorf("integration not found: %s", entry.IntegrationID), nil)
		return
	}
	// Resolve once and freeze the final class used by every policy gate and by
	// message_history. Payload tags remain authoritative; otherwise this is the
	// cached real-MX classification.
	if entry.Payload.VeridianProviderClass == "" {
		entry.Payload.VeridianProviderClass = w.veridianClassifyRecipient(entry)
	}

	// Veridian fork (correctif 2026-10-05, incident robertbrunon 05/10) — POOL
	// FAILOVER AT SEND TIME. The circuit breaker check and every coarse policy
	// gate below (exclusion, reputation, class throttle, daily cap, per-sender
	// cap, sending window) used to run ONCE against the integration frozen at
	// enqueue: a capped/unavailable integration simply rescheduled the entry
	// to tomorrow, even when a sibling in the workspace's rotation pool
	// (VeridianMarketingEmailProviderIDs) had room. They now run PER CANDIDATE,
	// in priority order (sequence continuity for automation relances, then the
	// assigned integration, then the rest of the pool) via
	// veridianSelectSendableIntegration — same gates, same thresholds, just
	// replayed for each pool member until one accepts. No pool configured =
	// exactly one candidate (the assigned integration): strict non-regression.
	// Cf. veridian_pool_failover.go.
	selection := w.veridianSelectSendableIntegration(workspace, entry, integration)

	if selection.Permanent {
		// Every reachable pool member excludes this recipient's class: same
		// permanent-failure contract as before (MarkAsProcessing first so
		// handleError, which assumes the attempt counter has advanced, deletes
		// the entry instead of leaving it half-processed).
		if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"error":    err.Error(),
			}).Warn("Failed to mark entry as processing for excluded-class skip")
			return
		}
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"recipient":      entry.ContactEmail,
			"provider_class": selection.ExcludedClass,
		}).Info("Excluded provider class on every reachable pool member, skipping SMTP and failing permanently")
		excludedErr := &emailerror.ClassifiedError{
			Original:  fmt.Errorf("excluded_provider_class:%s", selection.ExcludedClass),
			Type:      emailerror.ErrorTypeRecipient,
			Provider:  string(integration.EmailProvider.Kind),
			Retryable: false,
		}
		w.handleError(workspace, entry, excludedErr, excludedErr)
		return
	}

	if selection.Candidate == nil {
		// No pool member currently has room for this entry (every gate, or the
		// circuit breaker, rejected every reachable candidate). Reschedule
		// WITHOUT incrementing attempts, same contract as every coarse gate
		// this fork replaced — the shortest delay observed across candidates
		// is used so the next re-check happens as soon as the most promising
		// one might open up.
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"integration_id": entry.IntegrationID,
			"retry_in":       selection.RetryDelay.String(),
		}).Debug("No pool member has room for this entry, rescheduling without attempt increment")
		// Fiche 62 : le report porte desormais sa RAISON (la vraie : toutes les portes
		// ont ete evaluees) et la decision est journalisee selon le niveau du workspace.
		w.veridianDeferEntry(workspace, entry, veridianDeferral{
			Reason:  selection.Reason,
			Detail:  selection.ReasonDetail,
			Profile: selection.ReasonProfile,
			Delay:   selection.RetryDelay,
		}, &selection, func() error {
			return w.queueRepo.SetNextRetry(w.ctx, workspace.ID, entry.ID, time.Now().Add(selection.RetryDelay))
		})
		return
	}

	// A pool member accepted this entry: commit it. Rewriting Payload.FromAddress
	// also rewrites, downstream, the Message-ID host (veridianMessageIDForSend
	// derives it from FromAddress) and the DKIM signature applied by THIS
	// integration's own relay — integration, sender, Message-ID domain and DKIM
	// domain move together, exactly the fix for the bug this fork closes.
	chosen := selection.Candidate
	if chosen.IntegrationID != entry.IntegrationID {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":                entry.ID,
			"assigned_integration_id": entry.IntegrationID,
			"failover_integration_id": chosen.IntegrationID,
			"recipient":               entry.ContactEmail,
		}).Info("Pool failover: rerouting entry to a sibling integration with room")
	}
	entry.IntegrationID = chosen.IntegrationID
	entry.Payload.FromAddress = chosen.FromAddress
	entry.Payload.FromName = chosen.FromName
	if resolved := workspace.GetIntegrationByID(chosen.IntegrationID); resolved != nil {
		integration = resolved
	}
	entry.ProviderKind = integration.EmailProvider.Kind

	// Veridian fork (Lot 7): PRE-FILTER gate (cold outbound). Skips addresses we
	// KNOW are dead (invalid syntax / disposable domain / DNS-undeliverable)
	// BEFORE hitting SMTP, to protect IP reputation and quota. Unlike the
	// throttle/cap gates above, an invalid address never becomes valid: we route
	// it to PERMANENT failure (not a reschedule) via the same path as a
	// non-retryable `550 user unknown` provider error, so it is never re-tried in
	// a loop. Best-effort STRICT: any indeterminate verdict lets the send through.
	// Cf. veridian_prefilter.go.
	if reason, invalid := w.veridianPrefilterRecipient(entry); invalid {
		// MarkAsProcessing first (increments attempts) so handleError, which
		// assumes the attempt counter has already advanced, deletes the entry as
		// a permanent failure instead of leaving it half-processed.
		if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"error":    err.Error(),
			}).Warn("Failed to mark entry as processing for pre-filter skip")
			return
		}
		w.logger.WithFields(map[string]interface{}{
			"entry_id":  entry.ID,
			"recipient": entry.ContactEmail,
			"reason":    string(reason),
		}).Info("Pre-filtered recipient, skipping SMTP and failing permanently")
		// Non-retryable recipient error → handleError marks it permanent (delete
		// + message_history FailedAt). No SMTP connection is ever opened.
		prefilterErr := &emailerror.ClassifiedError{
			Original:  fmt.Errorf("pre-filtered recipient: %s", reason),
			Type:      emailerror.ErrorTypeRecipient,
			Provider:  string(integration.EmailProvider.Kind),
			Retryable: false,
		}
		w.handleError(workspace, entry, prefilterErr, prefilterErr)
		return
	}

	// Veridian fork: RENDU AU DEPILAGE. Le contenu (sujet, texte, html) est
	// re-rendu depuis le modele COURANT et le contact COURANT juste avant
	// l'envoi ; un echec de rendu = le message ne part pas (echec lisible).
	// Cf. veridian_render_at_send.go.
	if !w.veridianRenderAtSend(workspace, entry) {
		return
	}

	// Veridian fork: ANTI-HASH residual net (cold outbound). Best-effort,
	// LOG-ONLY filet : la variété est garantie à l'enqueue (re-spin) ; ce gate ne
	// fait que CONSTATER + TRACER une collision résiduelle (template sans spintax)
	// sans jamais bloquer l'envoi (pas de perte de mail). No-op sans hash sur le
	// payload. Placé après le pré-filtre, avant MarkAsProcessing.
	// Cf. veridian_content_hash_gate.go.
	w.veridianContentHashGate(workspace, &integration.EmailProvider, entry)

	// Wait for rate limiter - always use current integration rate limit (not stale payload value).
	// Veridian fork — alignement de capacité multi-SMTP : avec N senders actifs, le
	// débit global de l'infra = RateLimitPerMinute · N (chaque boîte porte sa part,
	// elles envoient en parallèle). VeridianEffectiveRateLimit renvoie le rate
	// inchangé pour 0/1 sender (non-régression). Le throttle PAR CLASSE
	// (veridianProviderClassGate) reste appliqué en amont et borne, lui, la pression
	// vers chaque provider destinataire indépendamment du nombre de senders.
	ratePerMinute := integration.EmailProvider.VeridianEffectiveRateLimit()
	if ratePerMinute <= 0 {
		ratePerMinute = 60 // Default to 1 per second if not configured
	}

	if err := w.rateLimiter.Wait(w.ctx, entry.IntegrationID, ratePerMinute); err != nil {
		// Context cancelled, don't mark as failed
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"error":    err.Error(),
		}).Debug("Rate limit wait cancelled")
		return
	}

	// Last possible policy check before constructing the provider request and
	// opening SMTP. This closes the race where the row was enqueued or even
	// claimed before a reply, unsubscribe or automation pause was persisted.
	if !w.veridianAutomationSendAllowed(workspace, entry) {
		return
	}
	if !w.veridianBroadcastSendAllowed(workspace, entry) {
		return
	}

	// Claim the queue row before reserving quota. This serializes duplicate
	// workers for the same entry; the dedicated refund below returns a
	// quota-blocked row to pending without consuming a delivery attempt.
	if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"error":    err.Error(),
		}).Warn("Failed to mark entry as processing, may be processed by another worker")
		return
	}

	// Atomic daily quota authorization is the LAST database gate before SMTP.
	// Exclusions, prefilter, window and final automation checks never consume it.
	quotaLeases, delay, capped := w.veridianReserveDailyQuota(workspace, &integration.EmailProvider, entry)
	if capped {
		w.veridianDeferEntry(workspace, entry, veridianDeferral{
			Reason:        domain.VeridianReasonQuotaDenied,
			Profile:       entry.IntegrationID,
			Delay:         delay,
			RefundAttempt: true,
		}, &selection, func() error {
			return w.queueRepo.SetNextRetryAndRefundAttempt(w.ctx, workspace.ID, entry.ID, time.Now().Add(delay))
		})
		return
	}

	// Build the send request
	request := entry.Payload.ToSendEmailProviderRequest(
		workspace.ID,
		entry.IntegrationID,
		entry.MessageID,
		entry.ContactEmail,
		&integration.EmailProvider,
	)

	// Send the email
	err := w.emailService.SendEmail(w.ctx, *request, true) // isMarketing = true
	if err != nil {
		// Release only when the transport proves the remote did not accept DATA.
		// Ambiguous post-DATA timeouts keep capacity consumed to avoid oversending.
		if emailerror.IsBeforeAcceptance(err) {
			w.veridianReleaseDailyQuotas(workspace.ID, quotaLeases)
		}
		// Classify the error
		classifiedErr := w.errorClassifier.Classify(err, integration.EmailProvider.Kind)

		// Log the classification for debugging
		w.logger.WithFields(map[string]interface{}{
			"entry_id":    entry.ID,
			"error_type":  classifiedErr.Type,
			"provider":    classifiedErr.Provider,
			"http_status": classifiedErr.HTTPStatus,
			"retryable":   classifiedErr.Retryable,
			"original":    err.Error(),
		}).Debug("Classified send error")

		// Record failure to circuit breaker (only counts provider errors)
		w.circuitBreaker.RecordFailure(entry.IntegrationID, classifiedErr)

		w.handleError(workspace, entry, err, classifiedErr)
		return
	}

	// Record success to reset circuit breaker
	w.circuitBreaker.RecordSuccess(entry.IntegrationID)

	// Mark as sent
	if err := w.queueRepo.MarkAsSent(w.ctx, workspace.ID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"error":    err.Error(),
		}).Error("Failed to mark email as sent")
		return
	}

	// Upsert message history (success - clears any previous failure)
	w.upsertMessageHistory(w.ctx, workspace.ID, workspace.Settings.SecretKey, entry, nil)

	// Fiche 62 : journal des decisions, l'envoi accepte avec les portes qui l'ont laisse passer.
	w.veridianRecordTerminal(workspace, entry, domain.VeridianOutcomeSent, "", "", entry.IntegrationID, &selection)

	w.logger.WithFields(map[string]interface{}{
		"entry_id":       entry.ID,
		"integration_id": entry.IntegrationID,
		"message_id":     entry.MessageID,
		"recipient":      entry.ContactEmail,
		"source_type":    entry.SourceType,
		"source_id":      entry.SourceID,
		"workspace_id":   workspace.ID,
	}).Debug("Email sent successfully")

	// Call success callback
	if w.onEmailSent != nil {
		w.onEmailSent(workspace.ID, entry.SourceType, entry.SourceID, entry.ContactEmail, entry.MessageID)
	}
}

// handleError handles a send error, scheduling retry or deleting permanently failed entries
// classifiedErr may be nil for internal errors (e.g., integration not found)
func (w *EmailQueueWorker) handleError(workspace *domain.Workspace, entry *domain.EmailQueueEntry, sendErr error, classifiedErr *emailerror.ClassifiedError) {
	entry.Attempts++ // Increment since MarkAsProcessing already did this

	// Determine if this is a permanent failure (non-retryable recipient error or max attempts)
	isPermanent := entry.Attempts >= entry.MaxAttempts
	if classifiedErr != nil && !classifiedErr.Retryable {
		isPermanent = true
	}

	logFields := map[string]interface{}{
		"entry_id":     entry.ID,
		"message_id":   entry.MessageID,
		"recipient":    entry.ContactEmail,
		"attempts":     entry.Attempts,
		"max_attempts": entry.MaxAttempts,
		"error":        sendErr.Error(),
		"is_permanent": isPermanent,
	}
	if classifiedErr != nil {
		logFields["error_type"] = classifiedErr.Type
	}
	w.logger.WithFields(logFields).Warn("Failed to send email")

	// Upsert message history with failure info
	w.upsertMessageHistory(w.ctx, workspace.ID, workspace.Settings.SecretKey, entry, sendErr)

	// Fiche 62 : journal des decisions (echec definitif, ou tentative qui sera rejouee).
	failReason := veridianFailureReason(sendErr)
	if !isPermanent {
		failReason = domain.VeridianReasonSendError
	}
	w.veridianRecordTerminal(workspace, entry, domain.VeridianOutcomeFailed, failReason, sendErr.Error(), entry.IntegrationID, nil)

	if isPermanent {
		// Permanent failure - delete the queue entry
		// Message history already tracks this permanent failure via upsertMessageHistory above
		w.logger.WithFields(map[string]interface{}{
			"entry_id":   entry.ID,
			"message_id": entry.MessageID,
			"attempts":   entry.Attempts,
		}).Warn("Email permanently failed")

		if err := w.queueRepo.Delete(w.ctx, workspace.ID, entry.ID); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"error":    err.Error(),
			}).Error("Failed to delete permanently failed queue entry")
		}

		// Call failure callback (isPermanent = true)
		if w.onEmailFailed != nil {
			w.onEmailFailed(workspace.ID, entry.SourceType, entry.SourceID, entry.ContactEmail, entry.MessageID, sendErr, true)
		}
		return
	}

	// Schedule retry with exponential backoff
	nextRetry := domain.CalculateNextRetryTime(entry.Attempts)
	if err := w.queueRepo.MarkAsFailed(w.ctx, workspace.ID, entry.ID, sendErr.Error(), &nextRetry); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"error":    err.Error(),
		}).Error("Failed to mark as failed for retry")
	}

	// Call failure callback (isPermanent = false, will retry)
	if w.onEmailFailed != nil {
		w.onEmailFailed(workspace.ID, entry.SourceType, entry.SourceID, entry.ContactEmail, entry.MessageID, sendErr, false)
	}
}

// upsertMessageHistory creates or updates a message history record after a send attempt
// On success: FailedAt and StatusInfo are nil (clears any previous failure)
// On failure: FailedAt is set to now, StatusInfo contains the error
func (w *EmailQueueWorker) upsertMessageHistory(
	ctx context.Context,
	workspaceID string,
	secretKey string,
	entry *domain.EmailQueueEntry,
	sendErr error,
) {
	now := time.Now().UTC()

	message := &domain.MessageHistory{
		ID:              entry.MessageID,
		ContactEmail:    entry.ContactEmail,
		TemplateID:      entry.TemplateID,
		TemplateVersion: int64(entry.Payload.TemplateVersion),
		Channel:         "email",
		MessageData:     domain.MessageData{Data: entry.Payload.TemplateData}, // Include template data for logging
		// sent_at is the source of truth for daily cold caps AND for every "messages
		// envoyés" stat (SUM(sent_at IS NOT NULL), webhook email.sent trigger). It
		// must be posed ONLY when sendErr == nil, i.e. the SMTP transaction was
		// actually accepted (250 after DATA) — never for a gate rejection, a
		// pre-filter skip or a real SMTP failure. Cf. incident robertbrunon 28-29/09
		// (36 message_history rows with sent_at posé alongside failed_at, 0-3 réels
		// sur les relais). Set conditionally below, after the failure branch.
		CreatedAt: entry.CreatedAt,
		UpdatedAt: now,
		// Veridian fork — anti-hash identique cold outbound : persiste le hash du
		// rendu final (posé à l'enqueue) pour alimenter la fenêtre glissante de
		// déduplication par classe. Vide pour les envois non-cold → stocké NULL.
		VeridianContentHash: entry.Payload.VeridianContentHash,
		// Veridian fork — adresse ÉMETTRICE (FROM) de l'envoi : persistée (V53)
		// pour alimenter le COUNT du plafond journalier par sender (warmup IP).
		// FromAddress est figé dans le payload à l'enqueue (sender-rotation incluse).
		// Vide pour les envois sans FROM connu → stocké NULL (hors index partiel).
		VeridianSenderEmail: entry.Payload.FromAddress,
		// V55: exact final class used by policy gates (payload tag or cached MX).
		VeridianProviderClass: entry.Payload.VeridianProviderClass,
		// V56: exact integration/profile attribution for daily caps and usage.
		VeridianProfileID: entry.IntegrationID,
	}

	// Lot 4 : un mail transactionnel n'est pas attribue a une adresse emettrice ni a
	// une classe de destinataire (aucun compteur commercial ne doit le voir), et
	// porte son type pour les metriques separees.
	if entry.Payload.VeridianTransactional {
		message.VeridianMessageType = domain.VeridianMessageTypeTransactional
		message.VeridianSenderEmail = ""
		message.VeridianProviderClass = ""
	}

	// Set source (broadcast or automation)
	if entry.SourceType == domain.EmailQueueSourceBroadcast {
		message.BroadcastID = &entry.SourceID
		if entry.Payload.ListID != "" {
			message.ListID = &entry.Payload.ListID
		}
	} else if entry.SourceType == domain.EmailQueueSourceAutomation {
		message.AutomationID = &entry.SourceID
	}

	// Set failure info if send failed (will be cleared on retry success via UPSERT)
	if sendErr != nil {
		message.FailedAt = &now
		errStr := sendErr.Error()
		if len(errStr) > 255 {
			errStr = errStr[:255]
		}
		message.StatusInfo = &errStr
	} else {
		// Only a real, accepted SMTP send sets sent_at. On UPSERT (ON CONFLICT DO
		// UPDATE SET sent_at = EXCLUDED.sent_at), a permanently-failed message is
		// deleted from the queue and never retried, so this branch never runs for
		// it; a retried message that eventually succeeds reaches this branch on its
		// successful attempt and correctly gets sent_at posed then, clearing the
		// prior failure via FailedAt/StatusInfo staying nil.
		message.SentAt = &now
	}

	// Upsert record (log errors but don't fail the send operation)
	if err := w.messageHistoryRepo.Upsert(ctx, workspaceID, secretKey, message); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":   entry.ID,
			"message_id": entry.MessageID,
			"error":      err.Error(),
		}).Warn("Failed to upsert message history")
	}
}

// GetStats returns statistics about the rate limiters
func (w *EmailQueueWorker) GetStats() map[string]RateLimiterStats {
	return w.rateLimiter.GetStats()
}

// GetConfig returns the worker configuration
func (w *EmailQueueWorker) GetConfig() *EmailQueueWorkerConfig {
	return w.config
}

// GetCircuitBreakerStats returns statistics about all circuit breakers
func (w *EmailQueueWorker) GetCircuitBreakerStats() map[string]CircuitBreakerStats {
	return w.circuitBreaker.GetStats()
}

// getMinEmailRateLimit returns the minimum rate limit across all email integrations
// Returns default of 60 if no email integrations found
func (w *EmailQueueWorker) getMinEmailRateLimit(workspace *domain.Workspace) int {
	emailIntegrations := workspace.GetIntegrationsByType(domain.IntegrationTypeEmail)
	if len(emailIntegrations) == 0 {
		return 60 // Default: 1 per second
	}

	// Veridian fork — capacité effective alignée sur le nombre de senders
	// (VeridianEffectiveRateLimit = rate · N senders ; inchangé pour 0/1 sender).
	// Le batch sizing dimensionne ainsi le fetch sur le débit agrégé réel.
	minRate := emailIntegrations[0].EmailProvider.VeridianEffectiveRateLimit()
	for _, integration := range emailIntegrations[1:] {
		if rate := integration.EmailProvider.VeridianEffectiveRateLimit(); rate < minRate {
			minRate = rate
		}
	}
	return minRate
}
