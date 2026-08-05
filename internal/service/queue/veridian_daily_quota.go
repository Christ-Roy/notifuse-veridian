package queue

import (
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

type veridianDailyQuotaLease struct {
	messageID string
	kind      string
	created   bool
}

// veridianReserveDailyQuota is the authoritative, atomic last-mile gate. The
// earlier COUNT gate remains a cheap skip optimization, but only this database
// reservation authorizes SMTP.
func (w *EmailQueueWorker) veridianReserveDailyQuota(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) ([]*veridianDailyQuotaLease, time.Duration, bool) {
	classCaps, _ := veridianResolveDailyCaps(workspace, provider, entry)
	now := time.Now().UTC()
	warmupCap := veridianWarmupCap(provider, now)
	class := entry.Payload.VeridianProviderClass
	if class == "" {
		class = w.veridianClassifyRecipient(entry)
		entry.Payload.VeridianProviderClass = class
	}

	classCap := 0
	if configured, ok := classCaps[class]; ok && configured > 0 {
		classCap = configured
	}
	if classCap <= 0 && warmupCap <= 0 {
		return nil, 0, false
	}

	senderDomain := veridianEmailDomain(entry.Payload.FromAddress)
	if senderDomain == "" {
		// V55 keys every reputation quota by the actual sender domain. Falling
		// back to a workspace-global wildcard would merge unrelated infras.
		w.logger.WithField("entry_id", entry.ID).Error("Daily quota has no sender domain; SMTP blocked")
		return nil, veridianDailyCapRecheckInterval, true
	}

	quotaRepo, ok := w.messageHistoryRepo.(domain.VeridianDailyQuotaRepository)
	if !ok {
		w.logger.WithField("entry_id", entry.ID).Error("Atomic daily quota repository unavailable; SMTP blocked")
		return nil, veridianDailyCapRecheckInterval, true
	}
	if classCap > 0 {
		if err := w.veridianBackfillDailyProviderClasses(quotaRepo, workspace.ID, now); err != nil {
			w.logger.WithFields(map[string]interface{}{"entry_id": entry.ID, "error": err.Error()}).Error("Daily quota provider-class backfill failed; SMTP blocked")
			return nil, veridianDailyCapRecheckInterval, true
		}
	}

	messageID := entry.MessageID
	if messageID == "" {
		messageID = entry.ID
	}
	specs := make([]domain.VeridianDailyQuotaReservation, 0, 2)
	if classCap > 0 {
		specs = append(specs, domain.VeridianDailyQuotaReservation{
			MessageID: messageID,
			Cap:       classCap,
			Key:       domain.VeridianDailyQuotaKey{WorkspaceID: workspace.ID, Day: veridianStartOfDayUTC(now), Kind: domain.VeridianDailyQuotaKindProviderClass, SenderDomain: senderDomain, ProviderClass: class},
		})
	}
	if warmupCap > 0 {
		specs = append(specs, domain.VeridianDailyQuotaReservation{
			MessageID: messageID,
			Cap:       warmupCap,
			Key:       domain.VeridianDailyQuotaKey{WorkspaceID: workspace.ID, Day: veridianStartOfDayUTC(now), Kind: domain.VeridianDailyQuotaKindWarmup, SenderDomain: senderDomain},
		})
	}

	leases := make([]*veridianDailyQuotaLease, 0, len(specs))
	for _, reservation := range specs {
		result, err := quotaRepo.ReserveDailyQuota(w.ctx, workspace.ID, reservation)
		if err != nil || !result.Reserved {
			w.veridianReleaseDailyQuotas(workspace.ID, leases)
			if err != nil {
				w.logger.WithFields(map[string]interface{}{"entry_id": entry.ID, "quota_kind": reservation.Key.Kind, "error": err.Error()}).Error("Atomic daily quota reservation failed; SMTP blocked")
			} else {
				w.veridianRescheduleCapped(entry, reservation.Key.Kind, result.Used, reservation.Cap, reservation.Key.ProviderClass)
			}
			return nil, veridianDailyCapRecheckInterval, true
		}
		leases = append(leases, &veridianDailyQuotaLease{messageID: messageID, kind: reservation.Key.Kind, created: !result.AlreadyReserved})
	}
	return leases, 0, false
}

func (w *EmailQueueWorker) veridianBackfillDailyProviderClasses(repo domain.VeridianDailyQuotaRepository, workspaceID string, now time.Time) error {
	cacheKey := fmt.Sprintf("%s:%s", workspaceID, now.Format("2006-01-02"))
	if _, done := w.dailyQuotaBackfilled.Load(cacheKey); done {
		return nil
	}
	messages, err := repo.ListUnclassifiedSuccessfulMessagesSince(w.ctx, workspaceID, veridianStartOfDayUTC(now))
	if err != nil {
		return err
	}
	for _, message := range messages {
		class := w.providerMXClassifier.ClassifyEmail(w.ctx, message.ContactEmail)
		if class == "" {
			return fmt.Errorf("empty provider class for historical message %s", message.ID)
		}
		if err := repo.SetMessageProviderClassIfEmpty(w.ctx, workspaceID, message.ID, class); err != nil {
			return err
		}
	}
	w.dailyQuotaBackfilled.Store(cacheKey, struct{}{})
	return nil
}

func (w *EmailQueueWorker) veridianReleaseDailyQuotas(workspaceID string, leases []*veridianDailyQuotaLease) {
	repo, ok := w.messageHistoryRepo.(domain.VeridianDailyQuotaRepository)
	if !ok {
		return
	}
	for i := len(leases) - 1; i >= 0; i-- {
		lease := leases[i]
		if lease == nil || !lease.created {
			continue
		}
		if err := repo.ReleaseDailyQuota(w.ctx, workspaceID, lease.messageID, lease.kind); err != nil {
			w.logger.WithFields(map[string]interface{}{"message_id": lease.messageID, "quota_kind": lease.kind, "error": err.Error()}).Error("Failed to release daily quota reservation; capacity kept fail-safe")
		}
	}
}
