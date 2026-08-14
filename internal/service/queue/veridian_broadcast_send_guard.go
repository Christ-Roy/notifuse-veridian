package queue

import (
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

const broadcastGuardRetryDelay = time.Minute

// veridianBroadcastSendAllowed closes the race between enqueue and SMTP. A
// reply, bounce or unsubscribe received while a broadcast row is waiting must
// suppress that row even if the broadcast itself is still processed.
func (w *EmailQueueWorker) veridianBroadcastSendAllowed(workspace *domain.Workspace, entry *domain.EmailQueueEntry) bool {
	if entry.SourceType != domain.EmailQueueSourceBroadcast {
		return true
	}
	if !w.finalSendGuardsConfigured {
		return true
	}
	if w.contactListRepo == nil || w.contactReplyRepo == nil {
		w.retryBroadcastGuard(workspace.ID, entry, fmt.Errorf("broadcast final guard is not configured"))
		return false
	}

	email := strings.ToLower(strings.TrimSpace(entry.ContactEmail))
	replied, err := w.contactReplyRepo.HasReplied(w.ctx, workspace.ID, email)
	if err != nil {
		w.retryBroadcastGuard(workspace.ID, entry, fmt.Errorf("reply lookup: %w", err))
		return false
	}
	if replied {
		w.discardBroadcastEntry(workspace.ID, entry, "contact_replied")
		return false
	}

	listID := strings.TrimSpace(entry.Payload.ListID)
	if listID == "" {
		w.retryBroadcastGuard(workspace.ID, entry, fmt.Errorf("broadcast queue row has no list id"))
		return false
	}
	contactList, err := w.contactListRepo.GetContactListByIDs(w.ctx, workspace.ID, email, listID)
	if err != nil {
		if _, notFound := err.(*domain.ErrContactListNotFound); notFound {
			w.discardBroadcastEntry(workspace.ID, entry, "contact_not_on_list")
			return false
		}
		w.retryBroadcastGuard(workspace.ID, entry, fmt.Errorf("list status lookup: %w", err))
		return false
	}
	if contactList.Status != domain.ContactListStatusActive {
		w.discardBroadcastEntry(workspace.ID, entry, "list_status_"+string(contactList.Status))
		return false
	}
	return true
}

func (w *EmailQueueWorker) discardBroadcastEntry(workspaceID string, entry *domain.EmailQueueEntry, reason string) {
	if err := w.queueRepo.Delete(w.ctx, workspaceID, entry.ID); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID, "reason": reason, "error": err.Error(),
		}).Error("Broadcast final guard blocked SMTP but failed to delete queue row")
		return
	}
	w.logger.WithFields(map[string]interface{}{
		"entry_id": entry.ID, "broadcast_id": entry.SourceID,
		"recipient": entry.ContactEmail, "reason": reason,
	}).Info("Broadcast final guard blocked SMTP and discarded stale queue row")
}

func (w *EmailQueueWorker) retryBroadcastGuard(workspaceID string, entry *domain.EmailQueueEntry, guardErr error) {
	nextRetry := time.Now().UTC().Add(broadcastGuardRetryDelay)
	if err := w.queueRepo.MarkAsFailed(w.ctx, workspaceID, entry.ID, guardErr.Error(), &nextRetry); err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID, "error": err.Error(),
		}).Error("Broadcast final guard blocked SMTP but failed to schedule retry")
		return
	}
	w.logger.WithFields(map[string]interface{}{
		"entry_id": entry.ID, "error": guardErr.Error(),
	}).Warn("Broadcast final guard failed closed; SMTP blocked and row scheduled for retry")
}
