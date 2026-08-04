package queue

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

const automationGuardRetryDelay = time.Minute

// veridianAutomationSendAllowed is deliberately placed immediately before the
// provider call. Terminal business states delete the stale queue row without
// creating a false send/failure history. Read failures fail closed and retry.
func (w *EmailQueueWorker) veridianAutomationSendAllowed(workspace *domain.Workspace, entry *domain.EmailQueueEntry) bool {
	if entry.SourceType != domain.EmailQueueSourceAutomation {
		return true
	}
	if w.automationRepo == nil || w.contactListRepo == nil || w.contactReplyRepo == nil {
		w.retryAutomationGuard(workspace.ID, entry, fmt.Errorf("automation final guard is not configured"))
		return false
	}

	automation, err := w.automationRepo.GetByID(w.ctx, workspace.ID, entry.SourceID)
	if err != nil {
		var notFound *domain.ErrAutomationNotFound
		if errors.As(err, &notFound) {
			w.discardAutomationEntry(workspace.ID, entry, "automation_deleted")
			return false
		}
		w.retryAutomationGuard(workspace.ID, entry, fmt.Errorf("automation lookup: %w", err))
		return false
	}
	if automation.Status != domain.AutomationStatusLive {
		w.discardAutomationEntry(workspace.ID, entry, "automation_not_live")
		return false
	}

	email := strings.ToLower(strings.TrimSpace(entry.ContactEmail))
	replied, err := w.contactReplyRepo.HasReplied(w.ctx, workspace.ID, email)
	if err != nil {
		w.retryAutomationGuard(workspace.ID, entry, fmt.Errorf("reply lookup: %w", err))
		return false
	}
	if replied {
		w.discardAutomationEntry(workspace.ID, entry, "contact_replied")
		return false
	}

	listID := automation.ListID
	if listID == "" {
		listID = entry.Payload.ListID
	}
	if listID == "" {
		return true
	}
	contactList, err := w.contactListRepo.GetContactListByIDs(w.ctx, workspace.ID, email, listID)
	if err != nil {
		if _, notFound := err.(*domain.ErrContactListNotFound); notFound {
			w.discardAutomationEntry(workspace.ID, entry, "contact_not_on_list")
			return false
		}
		w.retryAutomationGuard(workspace.ID, entry, fmt.Errorf("list status lookup: %w", err))
		return false
	}
	if contactList.Status != domain.ContactListStatusActive {
		w.discardAutomationEntry(workspace.ID, entry, "list_status_"+string(contactList.Status))
		return false
	}

	return true
}

func (w *EmailQueueWorker) discardAutomationEntry(workspaceID string, entry *domain.EmailQueueEntry, reason string) {
	if err := w.queueRepo.Delete(w.ctx, workspaceID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"reason":   reason,
			"error":    err.Error(),
		}).Error("Automation final guard blocked SMTP but failed to delete queue row")
		return
	}
	w.logger.WithFields(map[string]interface{}{
		"entry_id":      entry.ID,
		"automation_id": entry.SourceID,
		"recipient":     entry.ContactEmail,
		"reason":        reason,
	}).Info("Automation final guard blocked SMTP and discarded stale queue row")
}

func (w *EmailQueueWorker) retryAutomationGuard(workspaceID string, entry *domain.EmailQueueEntry, guardErr error) {
	nextRetry := time.Now().UTC().Add(automationGuardRetryDelay)
	if err := w.queueRepo.MarkAsFailed(w.ctx, workspaceID, entry.ID, guardErr.Error(), &nextRetry); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID,
			"error":    err.Error(),
		}).Error("Automation final guard blocked SMTP but failed to schedule retry")
		return
	}
	w.logger.WithFields(map[string]interface{}{
		"entry_id": entry.ID,
		"error":    guardErr.Error(),
	}).Warn("Automation final guard failed closed; SMTP blocked and row scheduled for retry")
}
