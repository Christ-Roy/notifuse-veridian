package queue

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
)

func TestAutomationFinalGuard_QueuedRowNeverReachesSMTPSink(t *testing.T) {
	tests := []struct {
		name       string
		status     domain.AutomationStatus
		lookupErr  error
		replied    bool
		listStatus domain.ContactListStatus
	}{
		{name: "automation paused after enqueue", status: domain.AutomationStatusPaused},
		{name: "automation deleted after enqueue", lookupErr: &domain.ErrAutomationNotFound{ID: "auto-1"}},
		{name: "contact replied after enqueue", status: domain.AutomationStatusLive, replied: true},
		{name: "contact unsubscribed after enqueue", status: domain.AutomationStatusLive, listStatus: domain.ContactListStatusUnsubscribed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			queueRepo := mocks.NewMockEmailQueueRepository(ctrl)
			workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
			emailSink := mocks.NewMockEmailServiceInterface(ctrl)
			historyRepo := mocks.NewMockMessageHistoryRepository(ctrl)
			automationRepo := mocks.NewMockAutomationRepository(ctrl)
			contactListRepo := mocks.NewMockContactListRepository(ctrl)
			replyRepo := mocks.NewMockVeridianContactReplyRepository(ctrl)
			log := pkgmocks.NewMockLogger(ctrl)
			log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
			log.EXPECT().Info(gomock.Any()).AnyTimes()
			log.EXPECT().Warn(gomock.Any()).AnyTimes()
			log.EXPECT().Error(gomock.Any()).AnyTimes()
			log.EXPECT().Debug(gomock.Any()).AnyTimes()

			workspace := &domain.Workspace{
				ID: "ws-1",
				Integrations: []domain.Integration{{
					ID:            "smtp-1",
					EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 6000},
				}},
			}
			entry := &domain.EmailQueueEntry{
				ID: "queued-before-stop", Status: domain.EmailQueueStatusPending,
				SourceType: domain.EmailQueueSourceAutomation, SourceID: "auto-1",
				IntegrationID: "smtp-1", ContactEmail: "lead@gmail.com", MessageID: "msg-1",
				Payload:     domain.EmailQueuePayload{ListID: "list-1", RateLimitPerMinute: 6000},
				MaxAttempts: 3,
			}

			queueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", entry.ID).Return(nil)
			var automation *domain.Automation
			if tc.lookupErr == nil {
				automation = &domain.Automation{ID: "auto-1", Status: tc.status, ListID: "list-1"}
			}
			automationRepo.EXPECT().GetByID(gomock.Any(), "ws-1", "auto-1").Return(automation, tc.lookupErr)
			if tc.lookupErr == nil && tc.status == domain.AutomationStatusLive {
				replyRepo.EXPECT().HasReplied(gomock.Any(), "ws-1", "lead@gmail.com").Return(tc.replied, nil)
			}
			if tc.lookupErr == nil && tc.status == domain.AutomationStatusLive && !tc.replied {
				contactListRepo.EXPECT().GetContactListByIDs(gomock.Any(), "ws-1", "lead@gmail.com", "list-1").Return(&domain.ContactList{
					Email: "lead@gmail.com", ListID: "list-1", Status: tc.listStatus,
				}, nil)
			}
			queueRepo.EXPECT().Delete(gomock.Any(), "ws-1", entry.ID).Return(nil)
			// No SendEmail expectation is registered on emailSink. Any SMTP sink
			// call is therefore an immediate test failure.

			worker := NewEmailQueueWorker(queueRepo, workspaceRepo, emailSink, historyRepo, DefaultWorkerConfig(), log)
			worker.ctx = context.Background()
			worker.SetAutomationSendGuard(automationRepo, contactListRepo, replyRepo)
			worker.processEntry(workspace, entry)
		})
	}
}
