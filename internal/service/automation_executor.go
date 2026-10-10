package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

// AutomationExecutor processes contacts through automation workflows
type AutomationExecutor struct {
	automationRepo  domain.AutomationRepository
	contactRepo     domain.ContactRepository
	workspaceRepo   domain.WorkspaceRepository
	contactListRepo domain.ContactListRepository
	templateRepo    domain.TemplateRepository
	emailQueueRepo  domain.EmailQueueRepository
	messageRepo     domain.MessageHistoryRepository
	timelineRepo    domain.ContactTimelineRepository
	nodeExecutors   map[domain.NodeType]NodeExecutor
	logger          logger.Logger
	apiEndpoint     string

	// coldReplyChecker (Lot 3 cold outbound, optionnel) : signale qu'un contact a
	// répondu → exit de la cadence cold. nil = exit-on-reply OFF (exit-on-bounce
	// reste actif). Injecté via SetColdReplyChecker.
	coldReplyChecker ColdReplyChecker

	// replyBranchExecutor : référence typée au node executor reply_branch (Veridian),
	// pour pouvoir lui injecter le coldReplyChecker via SetColdReplyChecker (le map des
	// executors est construit avant l'injection du checker). nil si non construit.
	replyBranchExecutor *ReplyBranchNodeExecutor
}

// NewAutomationExecutor creates a new AutomationExecutor
func NewAutomationExecutor(
	automationRepo domain.AutomationRepository,
	contactRepo domain.ContactRepository,
	workspaceRepo domain.WorkspaceRepository,
	contactListRepo domain.ContactListRepository,
	listRepo domain.ListRepository,
	templateRepo domain.TemplateRepository,
	emailQueueRepo domain.EmailQueueRepository,
	messageRepo domain.MessageHistoryRepository,
	timelineRepo domain.ContactTimelineRepository,
	log logger.Logger,
	apiEndpoint string,
) *AutomationExecutor {
	qb := NewQueryBuilder()

	// Veridian reply_branch executor : checker injecté plus tard via SetColdReplyChecker
	// (le map est construit avant que le service Lot 3 soit disponible dans app.go).
	replyBranchExecutor := NewReplyBranchNodeExecutor(nil, log)

	executors := map[domain.NodeType]NodeExecutor{
		domain.NodeTypeTrigger:          NewTriggerNodeExecutor(),
		domain.NodeTypeDelay:            NewDelayNodeExecutor(),
		domain.NodeTypeEmail:            NewEmailNodeExecutor(emailQueueRepo, templateRepo, workspaceRepo, listRepo, contactListRepo, apiEndpoint, log),
		domain.NodeTypeBranch:           NewBranchNodeExecutor(qb, workspaceRepo),
		domain.NodeTypeFilter:           NewFilterNodeExecutor(qb, workspaceRepo),
		domain.NodeTypeAddToList:        NewAddToListNodeExecutor(contactListRepo),
		domain.NodeTypeRemoveFromList:   NewRemoveFromListNodeExecutor(contactListRepo),
		domain.NodeTypeABTest:           NewABTestNodeExecutor(),
		domain.NodeTypeWebhook:          NewWebhookNodeExecutor(log),
		domain.NodeTypeListStatusBranch: NewListStatusBranchNodeExecutor(contactListRepo),
		domain.NodeTypeReplyBranch:      replyBranchExecutor,
	}

	return &AutomationExecutor{
		automationRepo:      automationRepo,
		contactRepo:         contactRepo,
		workspaceRepo:       workspaceRepo,
		contactListRepo:     contactListRepo,
		templateRepo:        templateRepo,
		emailQueueRepo:      emailQueueRepo,
		messageRepo:         messageRepo,
		timelineRepo:        timelineRepo,
		nodeExecutors:       executors,
		replyBranchExecutor: replyBranchExecutor,
		logger:              log,
		apiEndpoint:         apiEndpoint,
	}
}

// SetWebhookSecretKey fournit au nœud webhook la passphrase serveur qui
// déchiffre les secrets au repos (config.Security.SecretKey).
func (e *AutomationExecutor) SetWebhookSecretKey(key string) {
	if w, ok := e.nodeExecutors[domain.NodeTypeWebhook].(*WebhookNodeExecutor); ok {
		w.SetSecretKey(key)
	}
}

// Execute processes a contact through their automation nodes until a delay or completion.
// It loops through multiple nodes in a single tick for efficiency, persisting state after each node.
func (e *AutomationExecutor) Execute(ctx context.Context, workspaceID string, contactAutomation *domain.ContactAutomation) error {
	// Get automation once (outside loop)
	automation, err := e.automationRepo.GetByID(ctx, workspaceID, contactAutomation.AutomationID)
	if err != nil {
		return e.handleError(ctx, workspaceID, contactAutomation, err, "failed to get automation")
	}

	// Check if automation is paused/not live
	// When paused, contacts stay frozen at their current node (they don't get exited)
	if automation.Status != domain.AutomationStatusLive {
		return nil
	}

	// Early exit if already completed (no current node) - avoid fetching contact unnecessarily
	if contactAutomation.CurrentNodeID == nil {
		return e.markAsCompleted(ctx, workspaceID, contactAutomation, "completed")
	}

	// Get contact data once (outside loop) - only if we have nodes to process
	contactData, err := e.contactRepo.GetContactByEmail(ctx, workspaceID, contactAutomation.ContactEmail)
	if err != nil {
		return e.handleError(ctx, workspaceID, contactAutomation, err, "failed to get contact")
	}

	// LOOP: Process nodes until delay, completion, or max iterations
	const maxNodesPerTick = 10
	for iterations := 0; iterations < maxNodesPerTick; iterations++ {

		// Get current node from embedded nodes
		node := automation.GetNodeByID(*contactAutomation.CurrentNodeID)
		if node == nil {
			return e.markAsExited(ctx, workspaceID, contactAutomation, "automation_node_deleted")
		}

		// Veridian cold outbound (Lot 9) — gate d'exit souverain AVANT de processer le
		// node : un prospect qui a répondu (Lot 3) ou bouncé sort de la cadence à ce tick,
		// même s'il dort dans un node delay (l'engine upstream ne checke le bounce qu'à un
		// email node, insuffisant pour une cadence cold). Best-effort : une erreur de check
		// ne fige jamais la cadence — on log et on laisse le contact avancer.
		if exitReason, coldErr := e.veridianColdExitReason(ctx, workspaceID, automation, contactAutomation.ContactEmail); coldErr != nil {
			e.logger.WithField("error", coldErr).Warn("cold exit check failed (best-effort, contact continues)")
		} else if exitReason != "" {
			return e.markAsExited(ctx, workspaceID, contactAutomation, exitReason)
		}

		// Get executor for node type
		executor, ok := e.nodeExecutors[node.Type]
		if !ok {
			return e.handleError(ctx, workspaceID, contactAutomation,
				fmt.Errorf("unsupported node type: %s", node.Type), "unsupported node type")
		}

		// Create node execution entry (processing)
		nodeExecution := e.createNodeExecution(contactAutomation, node, domain.NodeActionProcessing)
		nodeStartTime := time.Now()
		_ = e.automationRepo.CreateNodeExecution(ctx, workspaceID, nodeExecution)

		// Build context from previous node executions
		executionContext, err := e.buildContextFromNodeExecutions(ctx, workspaceID, contactAutomation.ID)
		if err != nil {
			e.logger.WithField("error", err).Warn("Failed to build context from node executions")
			executionContext = make(map[string]interface{})
		}

		// Execute the node
		params := NodeExecutionParams{
			WorkspaceID:      workspaceID,
			Contact:          contactAutomation,
			Node:             node,
			Automation:       automation,
			ContactData:      contactData,
			ExecutionContext: executionContext,
		}
		result, execErr := executor.Execute(ctx, params)

		// Handle execution error
		if execErr != nil {
			nodeExecution.Action = domain.NodeActionFailed
			nodeExecution.Error = strPtr(execErr.Error())
			completedAt := time.Now().UTC()
			nodeExecution.CompletedAt = &completedAt
			_ = e.automationRepo.UpdateNodeExecution(ctx, workspaceID, nodeExecution)
			return e.handleError(ctx, workspaceID, contactAutomation, execErr, "node execution failed")
		}

		// Update contact automation state
		contactAutomation.CurrentNodeID = result.NextNodeID
		contactAutomation.ScheduledAt = result.ScheduledAt
		if result.ExitReason != nil {
			contactAutomation.ExitReason = result.ExitReason
		}

		// Determine status (terminal node = completed, unless waiting for a delay)
		isTerminalNode := result.NextNodeID == nil && result.Status == domain.ContactAutomationStatusActive
		isWaitingDelay := result.ScheduledAt != nil && result.ScheduledAt.After(time.Now())
		if isTerminalNode && !isWaitingDelay {
			contactAutomation.Status = domain.ContactAutomationStatusCompleted
		} else {
			contactAutomation.Status = result.Status
		}

		// PERSIST STATE (critical for crash recovery)
		if err := e.automationRepo.UpdateContactAutomation(ctx, workspaceID, contactAutomation); err != nil {
			return e.handleError(ctx, workspaceID, contactAutomation, err, "failed to update contact automation")
		}

		// Update node execution to completed
		duration := time.Since(nodeStartTime).Milliseconds()
		nodeExecution.Action = domain.NodeActionCompleted
		completedAt := time.Now().UTC()
		nodeExecution.CompletedAt = &completedAt
		nodeExecution.DurationMs = &duration
		nodeExecution.Output = result.Output
		_ = e.automationRepo.UpdateNodeExecution(ctx, workspaceID, nodeExecution)

		// EXIT: Completed (terminal node reached)
		if contactAutomation.Status == domain.ContactAutomationStatusCompleted {
			if statErr := e.automationRepo.IncrementAutomationStat(ctx, workspaceID, automation.ID, "completed"); statErr != nil {
				e.logger.WithField("error", statErr.Error()).Warn("Failed to increment automation stat \"completed\"")
			}
			e.createAutomationEndEvent(ctx, workspaceID, contactAutomation, "completed")
			return nil
		}

		// EXIT: Exited (filter/branch exit)
		if contactAutomation.Status == domain.ContactAutomationStatusExited {
			if statErr := e.automationRepo.IncrementAutomationStat(ctx, workspaceID, automation.ID, "exited"); statErr != nil {
				e.logger.WithField("error", statErr.Error()).Warn("Failed to increment automation stat \"exited\"")
			}
			reason := "exited"
			if contactAutomation.ExitReason != nil {
				reason = *contactAutomation.ExitReason
			}
			e.createAutomationEndEvent(ctx, workspaceID, contactAutomation, reason)
			return nil
		}

		// EXIT: Sending (email node parked, waiting for the queue worker's
		// terminal callback - HandleEmailSent/HandleEmailFailed - to resolve it).
		// MUST return here: CurrentNodeID was NOT advanced (EmailNodeExecutor
		// points it back at itself), so looping again this tick would re-run the
		// same email node and enqueue a duplicate message. Veridian fix
		// 2026-09-29, cf. ContactAutomationStatusSending doc comment.
		if contactAutomation.Status == domain.ContactAutomationStatusSending {
			return nil
		}

		// EXIT: Delay node (ScheduledAt is in the future)
		if result.ScheduledAt != nil && result.ScheduledAt.After(time.Now()) {
			return nil
		}

		// CONTINUE: Process next node immediately
	}

	// Hit max iterations - remaining nodes picked up next tick
	// State already persisted, so this is safe
	return nil
}

// ProcessBatch processes a batch of scheduled contacts
func (e *AutomationExecutor) ProcessBatch(ctx context.Context, limit int) (int, error) {
	// Get scheduled contacts globally
	now := time.Now().UTC()
	contacts, err := e.automationRepo.GetScheduledContactAutomationsGlobal(ctx, now, limit)
	if err != nil {
		return 0, fmt.Errorf("failed to get scheduled contacts: %w", err)
	}

	if len(contacts) == 0 {
		return 0, nil
	}

	// Veridian cold outbound (follow-up prioritization) — quand la capacité d'envoi est
	// contrainte (caps provider, sending windows, senders limités), tout le batch ne sera
	// pas servi à ce tick : on priorise les follow-up dont la fenêtre se ferme le plus
	// (le plus en retard sur son échéance d'abord). Tri PUR sur ScheduledAt/now, zéro
	// nouvelle colonne ; non-régressif hors contrainte (cf. veridian_followup_prioritizer.go).
	veridianPrioritizeFollowups(contacts, now)

	processed := 0
	for _, ca := range contacts {
		if err := e.Execute(ctx, ca.WorkspaceID, &ca.ContactAutomation); err != nil {
			e.logger.WithFields(map[string]interface{}{
				"contact_email": ca.ContactEmail,
				"automation_id": ca.AutomationID,
				"workspace_id":  ca.WorkspaceID,
				"error":         err.Error(),
			}).Error("Failed to execute automation for contact")
			// Continue with other contacts
			continue
		}
		processed++
	}

	return processed, nil
}

// handleError handles an error during execution by updating retry count and status
func (e *AutomationExecutor) handleError(ctx context.Context, workspaceID string, ca *domain.ContactAutomation, err error, context string) error {
	ca.RetryCount++
	errStr := fmt.Sprintf("%s: %s", context, err.Error())
	ca.LastError = &errStr
	now := time.Now().UTC()
	ca.LastRetryAt = &now

	if ca.RetryCount >= ca.MaxRetries {
		ca.Status = domain.ContactAutomationStatusFailed
		if statErr := e.automationRepo.IncrementAutomationStat(ctx, workspaceID, ca.AutomationID, "failed"); statErr != nil {
			e.logger.WithField("error", statErr.Error()).Warn("Failed to increment automation stat \"failed\"")
		}

		e.createAutomationEndEvent(ctx, workspaceID, ca, "failed")

		e.logger.WithFields(map[string]interface{}{
			"contact_email": ca.ContactEmail,
			"automation_id": ca.AutomationID,
			"workspace_id":  workspaceID,
			"retry_count":   ca.RetryCount,
			"error":         errStr,
		}).Error("Automation execution failed after max retries")
	} else {
		// Exponential backoff: 1min, 2min, 4min, etc.
		backoff := time.Duration(1<<uint(ca.RetryCount)) * time.Minute
		nextRetry := time.Now().UTC().Add(backoff)
		ca.ScheduledAt = &nextRetry

		e.logger.WithFields(map[string]interface{}{
			"contact_email": ca.ContactEmail,
			"automation_id": ca.AutomationID,
			"workspace_id":  workspaceID,
			"retry_count":   ca.RetryCount,
			"next_retry":    nextRetry,
			"error":         errStr,
		}).Warn("Automation execution failed, scheduling retry")
	}

	// Log node execution entry with error
	if ca.CurrentNodeID != nil {
		entry := &domain.NodeExecution{
			ID:                  uuid.NewString(),
			ContactAutomationID: ca.ID,
			AutomationID:        ca.AutomationID,
			NodeID:              *ca.CurrentNodeID,
			NodeType:            domain.NodeTypeTrigger, // Placeholder - actual type not available in error context
			Action:              domain.NodeActionFailed,
			EnteredAt:           time.Now().UTC(),
			Error:               &errStr,
		}
		_ = e.automationRepo.CreateNodeExecution(ctx, workspaceID, entry)
	}

	return e.automationRepo.UpdateContactAutomation(ctx, workspaceID, ca)
}

// markAsCompleted marks a contact automation as completed
func (e *AutomationExecutor) markAsCompleted(ctx context.Context, workspaceID string, ca *domain.ContactAutomation, reason string) error {
	ca.Status = domain.ContactAutomationStatusCompleted
	ca.ScheduledAt = nil
	ca.ExitReason = &reason

	e.logger.WithFields(map[string]interface{}{
		"contact_email": ca.ContactEmail,
		"automation_id": ca.AutomationID,
		"workspace_id":  workspaceID,
		"reason":        reason,
	}).Info("Contact automation completed")

	if statErr := e.automationRepo.IncrementAutomationStat(ctx, workspaceID, ca.AutomationID, "completed"); statErr != nil {
		e.logger.WithField("error", statErr.Error()).Warn("Failed to increment automation stat \"completed\"")
	}

	e.createAutomationEndEvent(ctx, workspaceID, ca, reason)

	return e.automationRepo.UpdateContactAutomation(ctx, workspaceID, ca)
}

// markAsExited marks a contact automation as exited
func (e *AutomationExecutor) markAsExited(ctx context.Context, workspaceID string, ca *domain.ContactAutomation, reason string) error {
	ca.Status = domain.ContactAutomationStatusExited
	ca.ScheduledAt = nil
	ca.ExitReason = &reason

	e.logger.WithFields(map[string]interface{}{
		"contact_email": ca.ContactEmail,
		"automation_id": ca.AutomationID,
		"workspace_id":  workspaceID,
		"reason":        reason,
	}).Info("Contact automation exited")

	if statErr := e.automationRepo.IncrementAutomationStat(ctx, workspaceID, ca.AutomationID, "exited"); statErr != nil {
		e.logger.WithField("error", statErr.Error()).Warn("Failed to increment automation stat \"exited\"")
	}

	e.createAutomationEndEvent(ctx, workspaceID, ca, reason)

	return e.automationRepo.UpdateContactAutomation(ctx, workspaceID, ca)
}

// HandleEmailSent resolves a contact PARKED on an email node (status=sending,
// cf. ContactAutomationStatusSending) once the email queue worker confirms the
// SMTP send was truly accepted by our relay. Wired as the EmailQueueWorker's
// onSent callback (see SetCallbacks in internal/app/app.go). No-op for
// non-automation sources and for contacts that are not (or no longer) parked -
// e.g. a late/duplicate callback after the contact was already advanced or the
// automation was deleted/re-created underneath it.
// Veridian fix 2026-09-29 (todo/2026-09-29-automation-advance-on-send-only.md).
func (e *AutomationExecutor) HandleEmailSent(workspaceID string, sourceType domain.EmailQueueSourceType, sourceID, contactEmail, messageID string) {
	if sourceType != domain.EmailQueueSourceAutomation {
		return
	}
	ctx := context.Background()

	ca, err := e.automationRepo.GetContactAutomationByEmail(ctx, workspaceID, sourceID, contactEmail)
	if err != nil {
		e.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID, "automation_id": sourceID,
			"contact_email": contactEmail, "message_id": messageID, "error": err.Error(),
		}).Warn("HandleEmailSent: contact automation not found")
		return
	}
	if ca.Status != domain.ContactAutomationStatusSending || ca.CurrentNodeID == nil {
		// Already resolved by another callback, or never parked. Nothing to advance.
		return
	}

	automation, err := e.automationRepo.GetByID(ctx, workspaceID, sourceID)
	if err != nil {
		e.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID, "automation_id": sourceID, "error": err.Error(),
		}).Warn("HandleEmailSent: automation not found")
		return
	}
	node := automation.GetNodeByID(*ca.CurrentNodeID)
	if node == nil {
		_ = e.markAsExited(ctx, workspaceID, ca, "automation_node_deleted")
		return
	}

	// Log the email node as genuinely completed now that delivery is confirmed.
	entry := e.createNodeExecution(ca, node, domain.NodeActionCompleted)
	completedAt := time.Now().UTC()
	entry.CompletedAt = &completedAt
	entry.Output = buildNodeOutput(domain.NodeTypeEmail, map[string]interface{}{
		"message_id": messageID,
		"to":         contactEmail,
		"sent":       true,
	})
	_ = e.automationRepo.CreateNodeExecution(ctx, workspaceID, entry)

	if node.NextNodeID == nil {
		_ = e.markAsCompleted(ctx, workspaceID, ca, "completed")
		return
	}

	ca.CurrentNodeID = node.NextNodeID
	ca.Status = domain.ContactAutomationStatusActive
	now := time.Now().UTC()
	ca.ScheduledAt = &now
	if err := e.automationRepo.UpdateContactAutomation(ctx, workspaceID, ca); err != nil {
		e.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID, "automation_id": sourceID,
			"contact_email": contactEmail, "error": err.Error(),
		}).Error("HandleEmailSent: failed to advance contact automation")
	}
}

// HandleEmailFailed resolves a contact PARKED on an email node when the queue
// worker gives up on the send. isPermanent=true covers both a definitive policy
// rejection (excluded provider class, pre-filtered/invalid address, a
// non-retryable SMTP rejection) AND a retryable SMTP error that has exhausted
// its bounded retries (EmailQueueWorkerConfig.MaxRetries): either way the
// contact EXITS the automation with the failure as exit_reason - it never
// reaches a follow-up node. isPermanent=false (still retrying) is a deliberate
// no-op: the contact stays parked, the queue's own backoff will call back again
// on the next attempt. Wired as the EmailQueueWorker's onFailed callback (see
// SetCallbacks in internal/app/app.go).
// Veridian fix 2026-09-29 (todo/2026-09-29-automation-advance-on-send-only.md).
func (e *AutomationExecutor) HandleEmailFailed(workspaceID string, sourceType domain.EmailQueueSourceType, sourceID, contactEmail, messageID string, sendErr error, isPermanent bool) {
	if sourceType != domain.EmailQueueSourceAutomation || !isPermanent {
		return
	}
	ctx := context.Background()

	ca, err := e.automationRepo.GetContactAutomationByEmail(ctx, workspaceID, sourceID, contactEmail)
	if err != nil {
		e.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID, "automation_id": sourceID,
			"contact_email": contactEmail, "message_id": messageID, "error": err.Error(),
		}).Warn("HandleEmailFailed: contact automation not found")
		return
	}
	if ca.Status != domain.ContactAutomationStatusSending {
		return
	}

	reason := "send_failed"
	if sendErr != nil {
		reason = sendErr.Error()
	}

	if ca.CurrentNodeID != nil {
		if automation, aerr := e.automationRepo.GetByID(ctx, workspaceID, sourceID); aerr == nil {
			if node := automation.GetNodeByID(*ca.CurrentNodeID); node != nil {
				entry := e.createNodeExecution(ca, node, domain.NodeActionFailed)
				completedAt := time.Now().UTC()
				entry.CompletedAt = &completedAt
				errCopy := reason
				entry.Error = &errCopy
				entry.Output = buildNodeOutput(domain.NodeTypeEmail, map[string]interface{}{
					"message_id": messageID,
					"to":         contactEmail,
					"sent":       false,
					"reason":     reason,
				})
				_ = e.automationRepo.CreateNodeExecution(ctx, workspaceID, entry)
			}
		}
	}

	if err := e.markAsExited(ctx, workspaceID, ca, reason); err != nil {
		e.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID, "automation_id": sourceID,
			"contact_email": contactEmail, "error": err.Error(),
		}).Error("HandleEmailFailed: failed to exit contact automation")
	}
}

// createNodeExecution creates a new node execution entry for logging
func (e *AutomationExecutor) createNodeExecution(ca *domain.ContactAutomation, node *domain.AutomationNode, action domain.NodeAction) *domain.NodeExecution {
	return &domain.NodeExecution{
		ID:                  uuid.NewString(),
		ContactAutomationID: ca.ID,
		AutomationID:        ca.AutomationID,
		NodeID:              node.ID,
		NodeType:            node.Type,
		Action:              action,
		EnteredAt:           time.Now().UTC(),
		Output:              make(map[string]interface{}),
	}
}

// buildContextFromNodeExecutions reconstructs context from completed node executions
// This allows nodes to access data from previous nodes in the workflow
func (e *AutomationExecutor) buildContextFromNodeExecutions(ctx context.Context, workspaceID, contactAutomationID string) (map[string]interface{}, error) {
	entries, err := e.automationRepo.GetNodeExecutions(ctx, workspaceID, contactAutomationID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	for _, entry := range entries {
		if entry.Action == domain.NodeActionCompleted && entry.Output != nil {
			result[entry.NodeID] = entry.Output
		}
	}
	return result, nil
}

// createAutomationEndEvent creates an automation.end timeline event when a contact exits an automation
func (e *AutomationExecutor) createAutomationEndEvent(ctx context.Context, workspaceID string, ca *domain.ContactAutomation, exitReason string) {
	entry := &domain.ContactTimelineEntry{
		Email:      ca.ContactEmail,
		Operation:  "update",
		EntityType: "automation",
		Kind:       "automation.end",
		EntityID:   &ca.AutomationID,
		Changes: map[string]interface{}{
			"automation_id": map[string]interface{}{"new": ca.AutomationID},
			"exit_reason":   map[string]interface{}{"new": exitReason},
			"status":        map[string]interface{}{"new": string(ca.Status)},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := e.timelineRepo.Create(ctx, workspaceID, entry); err != nil {
		e.logger.WithFields(map[string]interface{}{
			"contact_email": ca.ContactEmail,
			"automation_id": ca.AutomationID,
			"error":         err.Error(),
		}).Warn("Failed to create automation.end timeline event")
	}
}
