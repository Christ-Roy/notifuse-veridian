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

			var automation *domain.Automation
			if tc.lookupErr == nil {
				automation = &domain.Automation{ID: "auto-1", Status: tc.status, ListID: "list-1"}
			}
			// La porte « premier mail livre » lit aussi l'historique et l'automation avant la
			// garde finale : historique vide, automation sans noeud (pas une relance), elle laisse passer.
			historyRepo.EXPECT().GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@gmail.com", gomock.Any(), gomock.Any()).Return(nil, 0, nil).AnyTimes()
			automationRepo.EXPECT().GetByID(gomock.Any(), "ws-1", "auto-1").Return(automation, tc.lookupErr).AnyTimes()
			if tc.lookupErr == nil && tc.status == domain.AutomationStatusLive {
				replyRepo.EXPECT().HasReplied(gomock.Any(), "ws-1", "lead@gmail.com").Return(tc.replied, nil)
			}
			if tc.lookupErr == nil && tc.status == domain.AutomationStatusLive && !tc.replied {
				contactListRepo.EXPECT().GetContactListByIDs(gomock.Any(), "ws-1", "lead@gmail.com", "list-1").Return(&domain.ContactList{
					Email: "lead@gmail.com", ListID: "list-1", Status: tc.listStatus,
				}, nil)
			}
			// Fiche 62 : une automation en PAUSE garde sa ligne (reportee, jamais supprimee
			// en silence : le contact resterait parque en « sending » a jamais) ; tous les
			// autres cas suppriment la ligne ET previennent l'executeur.
			paused := tc.lookupErr == nil && tc.status == domain.AutomationStatusPaused
			if paused {
				queueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", entry.ID, gomock.Any()).Return(nil)
			} else {
				queueRepo.EXPECT().Delete(gomock.Any(), "ws-1", entry.ID).Return(nil)
			}
			var failedCalls []string
			// No SendEmail expectation is registered on emailSink. Any SMTP sink
			// call is therefore an immediate test failure.

			worker := NewEmailQueueWorker(queueRepo, workspaceRepo, emailSink, historyRepo, DefaultWorkerConfig(), log)
			worker.ctx = context.Background()
			worker.SetAutomationSendGuard(automationRepo, contactListRepo, replyRepo)
			worker.SetCallbacks(nil, func(_ string, _ domain.EmailQueueSourceType, _ string, _ string, _ string, err error, permanent bool) {
				if permanent {
					failedCalls = append(failedCalls, err.Error())
				}
			})
			worker.processEntry(workspace, entry)
			if paused {
				if len(failedCalls) != 0 {
					t.Fatalf("a paused automation must not fail its contact, got %v", failedCalls)
				}
			} else if len(failedCalls) != 1 {
				t.Fatalf("the executor must be told the row was discarded (else the contact stays parked in sending), got %v", failedCalls)
			}
		})
	}
}

type fakeEnrollmentReader struct {
	ca  *domain.ContactAutomation
	err error
}

func (f fakeEnrollmentReader) GetContactAutomationByEmail(context.Context, string, string, string) (*domain.ContactAutomation, error) {
	return f.ca, f.err
}

// VER-220 : un contact sorti de sa séquence (rebond, réponse, sortie manuelle) APRÈS la mise en
// file ne reçoit pas le mail en attente ; une inscription en cours ou une lecture en échec ne
// bloque rien (comportement d'avant). Le mail ne touche jamais le relais quand il est bloqué.
func TestAutomationFinalGuard_ExitedEnrollmentNeverReachesSMTPSink(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reader  EnrollmentReader
		blocked bool
	}{
		{"sortie par rebond", fakeEnrollmentReader{ca: &domain.ContactAutomation{Status: domain.ContactAutomationStatusExited}}, true},
		{"séquence terminée", fakeEnrollmentReader{ca: &domain.ContactAutomation{Status: domain.ContactAutomationStatusCompleted}}, true},
		{"en échec", fakeEnrollmentReader{ca: &domain.ContactAutomation{Status: domain.ContactAutomationStatusFailed}}, true},
		{"toujours en cours", fakeEnrollmentReader{ca: &domain.ContactAutomation{Status: domain.ContactAutomationStatusSending}}, false},
		{"lecture en échec", fakeEnrollmentReader{err: context.DeadlineExceeded}, false},
		{"lecteur non branché", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetVeridianEnrollmentReader(tc.reader)
			t.Cleanup(func() { SetVeridianEnrollmentReader(nil) })
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			queueRepo := mocks.NewMockEmailQueueRepository(ctrl)
			automationRepo := mocks.NewMockAutomationRepository(ctrl)
			contactListRepo := mocks.NewMockContactListRepository(ctrl)
			replyRepo := mocks.NewMockVeridianContactReplyRepository(ctrl)
			log := pkgmocks.NewMockLogger(ctrl)
			log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
			log.EXPECT().Info(gomock.Any()).AnyTimes()
			log.EXPECT().Warn(gomock.Any()).AnyTimes()
			log.EXPECT().Error(gomock.Any()).AnyTimes()
			log.EXPECT().Debug(gomock.Any()).AnyTimes()

			entry := &domain.EmailQueueEntry{
				ID: "queued-before-exit", SourceType: domain.EmailQueueSourceAutomation, SourceID: "auto-1",
				IntegrationID: "smtp-1", ContactEmail: "lead@gmail.com", MessageID: "msg-1",
				Payload: domain.EmailQueuePayload{ListID: "list-1"},
			}
			automationRepo.EXPECT().GetByID(gomock.Any(), "ws-1", "auto-1").Return(&domain.Automation{ID: "auto-1", Status: domain.AutomationStatusLive, ListID: "list-1"}, nil).AnyTimes()
			replyRepo.EXPECT().HasReplied(gomock.Any(), "ws-1", "lead@gmail.com").Return(false, nil)
			if tc.blocked {
				queueRepo.EXPECT().Delete(gomock.Any(), "ws-1", entry.ID).Return(nil)
			} else {
				contactListRepo.EXPECT().GetContactListByIDs(gomock.Any(), "ws-1", "lead@gmail.com", "list-1").Return(&domain.ContactList{Status: domain.ContactListStatusActive}, nil)
			}
			worker := NewEmailQueueWorker(queueRepo, mocks.NewMockWorkspaceRepository(ctrl), mocks.NewMockEmailServiceInterface(ctrl), mocks.NewMockMessageHistoryRepository(ctrl), DefaultWorkerConfig(), log)
			worker.ctx = context.Background()
			worker.SetAutomationSendGuard(automationRepo, contactListRepo, replyRepo)
			var failed []string
			worker.SetCallbacks(nil, func(_ string, _ domain.EmailQueueSourceType, _, _, _ string, err error, permanent bool) {
				if permanent {
					failed = append(failed, err.Error())
				}
			})
			allowed := worker.veridianAutomationSendAllowed(&domain.Workspace{ID: "ws-1"}, entry)
			if allowed == tc.blocked {
				t.Fatalf("envoi autorisé=%v, bloqué attendu=%v", allowed, tc.blocked)
			}
			if tc.blocked && (len(failed) != 1 || failed[0] != "contact_"+string(tc.reader.(fakeEnrollmentReader).ca.Status)) {
				t.Fatalf("l'exécuteur doit savoir pourquoi la ligne est retirée: %v", failed)
			}
		})
	}
}
