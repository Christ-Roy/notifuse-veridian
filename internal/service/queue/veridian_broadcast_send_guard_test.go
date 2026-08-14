package queue

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
)

func TestBroadcastFinalGuard_StoppedContactNeverReachesSMTPSink(t *testing.T) {
	tests := []struct {
		name       string
		replied    bool
		listStatus domain.ContactListStatus
	}{
		{name: "reply after enqueue", replied: true},
		{name: "bounce after enqueue", listStatus: domain.ContactListStatusBounced},
		{name: "unsubscribe after enqueue", listStatus: domain.ContactListStatusUnsubscribed},
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

			workspace := &domain.Workspace{ID: "ws-1", Integrations: []domain.Integration{{
				ID: "smtp-1", EmailProvider: domain.EmailProvider{
					Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 6000,
				},
			}}}
			entry := &domain.EmailQueueEntry{
				ID: "queued-before-stop", Status: domain.EmailQueueStatusPending,
				SourceType: domain.EmailQueueSourceBroadcast, SourceID: "broadcast-1",
				IntegrationID: "smtp-1", ContactEmail: "lead@gmail.com", MessageID: "msg-1",
				Payload:     domain.EmailQueuePayload{ListID: "list-1", RateLimitPerMinute: 6000},
				MaxAttempts: 3,
			}

			replyRepo.EXPECT().HasReplied(gomock.Any(), "ws-1", "lead@gmail.com").Return(tc.replied, nil)
			if !tc.replied {
				contactListRepo.EXPECT().GetContactListByIDs(
					gomock.Any(), "ws-1", "lead@gmail.com", "list-1",
				).Return(&domain.ContactList{
					Email: "lead@gmail.com", ListID: "list-1", Status: tc.listStatus,
				}, nil)
			}
			queueRepo.EXPECT().Delete(gomock.Any(), "ws-1", entry.ID).Return(nil)
			// No SendEmail expectation: any SMTP call fails this test immediately.

			worker := NewEmailQueueWorker(queueRepo, workspaceRepo, emailSink, historyRepo, DefaultWorkerConfig(), log)
			worker.ctx = context.Background()
			worker.SetAutomationSendGuard(automationRepo, contactListRepo, replyRepo)
			worker.processEntry(workspace, entry)
		})
	}
}
