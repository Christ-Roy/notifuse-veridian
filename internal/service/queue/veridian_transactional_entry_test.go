package queue

import (
	"context"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/emailerror"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lot 4 (08/10/2026) : isolation du transactionnel dans le worker. Chaque test
// pose un profil commercial "nord" (dans la rotation) que TOUTES les portes
// bloqueraient (fenetre fermee, chauffe a 1 par jour, plafonds de classe, profil)
// et un profil transactionnel reserve "tx". Le mock de l'historique n'a AUCUNE
// attente de compteur : un seul appel a une porte commerciale fait echouer le test.

// veridianClosedWindow est une fenetre d'envoi qui n'est jamais ouverte maintenant.
func veridianClosedWindow() *domain.VeridianSendingWindow {
	h := (time.Now().UTC().Hour() + 12) % 24
	return &domain.VeridianSendingWindow{StartHour: h, EndHour: h + 1, Timezone: "UTC"}
}

func transactionalTestWorkspace(reserveTransactional bool) *domain.Workspace {
	nord := veridianTestPoolIntegration("nord", "hello@nord.example")
	now := time.Now().UTC()
	nord.EmailProvider.VeridianSendingWindow = veridianClosedWindow()
	nord.EmailProvider.VeridianWarmupStartedAt = &now
	nord.EmailProvider.VeridianWarmupSchedule = []int{1}
	nord.EmailProvider.VeridianProfileDailyCap = 1
	tx := veridianTestPoolIntegration("tx", "no-reply@tx.example")
	tx.EmailProvider.RateLimitPerMinute = 6000
	ws := &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			VeridianMarketingEmailProviderIDs: []string{"nord"},
			MarketingEmailProviderID:          "nord",
			VeridianProviderClassDailyCap:     map[string]int{"google": 1},
			VeridianPerRecipientDailyCap:      1,
		},
		Integrations: []domain.Integration{nord, tx},
	}
	if reserveTransactional {
		ws.Settings.TransactionalEmailProviderID = "tx"
	}
	return ws
}

func transactionalTestEntry(integrationID string) *domain.EmailQueueEntry {
	entry := veridianTestEntry("t1", "client@gmail.com", domain.EmailQueuePayload{
		FromAddress: "no-reply@tx.example", FromName: "Notifications",
		Subject: "Votre commande", HTMLContent: "<p>ok</p>", TextContent: "ok",
	})
	entry.SourceType = domain.EmailQueueSourceAutomation
	entry.SourceID = "auto-1"
	entry.IntegrationID = integrationID
	entry.Payload.VeridianTransactional = true
	return entry
}

func TestTransactionalEntry_LeavesThroughTheReservedProfileIgnoringEveryCommercialGate(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := transactionalTestWorkspace(true)
	veridianAllowAutomationSend(t, env, "auto-1", "client@gmail.com")
	entry := transactionalTestEntry("tx")

	var sent domain.SendEmailProviderRequest
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "t1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "t1").Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), false).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error { sent = req; return nil })
	var row *domain.MessageHistory
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error { row = m; return nil })
	// Ni report, ni compteur, ni reservation de quota : aucune autre attente.
	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(ws, entry)

	assert.Equal(t, "tx", sent.IntegrationID, "un mail transactionnel part par le profil transactionnel")
	assert.Equal(t, "no-reply@tx.example", sent.FromAddress)
	require.NotNil(t, row)
	assert.Equal(t, domain.VeridianMessageTypeTransactional, row.VeridianMessageType)
	assert.Equal(t, "tx", row.VeridianProfileID)
	assert.Empty(t, row.VeridianSenderEmail, "aucune adresse emettrice : le transactionnel n'entre dans aucun compteur d'adresse ou de domaine")
	assert.Empty(t, row.VeridianProviderClass)
}

func TestTransactionalEntry_IsReroutedToTheReservedProfileNeverTheRotation(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := transactionalTestWorkspace(true)
	veridianAllowAutomationSend(t, env, "auto-1", "client@gmail.com")
	// Mise en file avant la reservation : integration memorisee = un profil commercial.
	entry := transactionalTestEntry("nord")
	entry.Payload.FromAddress, entry.Payload.FromName = "hello@nord.example", "Veridian"

	var sent domain.SendEmailProviderRequest
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "t1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "t1").Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), false).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error { sent = req; return nil })
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

	env.worker.processEntry(ws, entry)

	assert.Equal(t, "tx", sent.IntegrationID)
	assert.Equal(t, "no-reply@tx.example", sent.FromAddress, "integration, expediteur, Message-ID et DKIM suivent le profil transactionnel")
	assert.Equal(t, "tx", entry.IntegrationID)
}

func TestTransactionalEntry_WithoutReservedProfileKeepsTheNativeCommercialPath(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := transactionalTestWorkspace(false) // pas de profil transactionnel : comportement natif
	veridianAllowAutomationSend(t, env, "auto-1", "client@gmail.com")
	entry := transactionalTestEntry("nord")
	entry.Payload.FromAddress = "hello@nord.example"
	veridianExpectHealthyReputation(env, "ws-1", "nord.example")
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "client@gmail.com", gomock.Any(), gomock.Any()).Return(nil, 0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForContact(gomock.Any(), "ws-1", "client@gmail.com", gomock.Any()).Return(0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", gomock.Any(), "nord.example", gomock.Any()).Return(0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "nord.example", gomock.Any()).Return(0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSender(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(0, nil).AnyTimes()

	// Fenetre fermee du profil commercial : l'entree est reportee, aucun envoi.
	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "t1", gomock.Any()).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(ws, entry)

	assert.False(t, entry.Payload.VeridianTransactional, "sans profil reserve le marqueur est ignore (la ligne ne sera pas typee transactionnelle)")
}

func TestCommercialEntryAssignedToTheTransactionalProfileLeavesThroughTheRotationInstead(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := transactionalTestWorkspace(true)
	nord := &ws.Integrations[0]
	nord.EmailProvider.VeridianSendingWindow = nil // la rotation est ouverte
	nord.EmailProvider.VeridianWarmupStartedAt = nil
	nord.EmailProvider.VeridianWarmupSchedule = nil
	nord.EmailProvider.VeridianProfileDailyCap = 0
	ws.Settings.VeridianProviderClassDailyCap = nil
	ws.Settings.VeridianPerRecipientDailyCap = 0

	// Entree commerciale dont le profil memorise est le profil transactionnel (noeud de
	// sequence regle dessus, ou ancien profil par defaut).
	entry := veridianTestEntry("c1", "lead@gmail.com", domain.EmailQueuePayload{FromAddress: "no-reply@tx.example", FromName: "Notifications"})
	entry.IntegrationID = "tx"
	veridianExpectHealthyReputation(env, "ws-1", "nord.example")
	env.worker.messageHistoryRepo = newVeridianPoolQuotaRepo(env)

	var sent domain.SendEmailProviderRequest
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "c1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "c1").Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error { sent = req; return nil })
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

	env.worker.processEntry(ws, entry)

	assert.Equal(t, "nord", sent.IntegrationID, "la rotation commerciale n'emprunte jamais le profil transactionnel")
	assert.Equal(t, "hello@nord.example", sent.FromAddress)
}

func TestCommercialEntryWaitsWhenNoCommercialProfileExistsBesidesTheTransactionalOne(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	tx := veridianTestPoolIntegration("tx", "no-reply@tx.example")
	ws := &domain.Workspace{
		ID:           "ws-1",
		Settings:     domain.WorkspaceSettings{TransactionalEmailProviderID: "tx"},
		Integrations: []domain.Integration{tx},
	}
	entry := veridianTestEntry("c1", "lead@gmail.com", domain.EmailQueuePayload{FromAddress: "no-reply@tx.example"})
	entry.IntegrationID = "tx"

	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "c1", gomock.Any()).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(ws, entry)
}

func TestTransactionalEntry_OnlyAnOpenCircuitOfTheProfileHoldsItBack(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := transactionalTestWorkspace(true)
	veridianAllowAutomationSend(t, env, "auto-1", "client@gmail.com")
	for i := 0; i < 5; i++ {
		env.worker.circuitBreaker.RecordFailure("tx", &emailerror.ClassifiedError{Type: emailerror.ErrorTypeProvider})
	}
	entry := transactionalTestEntry("tx")

	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "t1", gomock.Any()).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(ws, entry)
}

func TestTransactionalEntry_ContactReplyDoesNotStopIt_ButADeadSequenceDoes(t *testing.T) {
	t.Run("une reponse recue ne retient pas un transactionnel", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		ws := transactionalTestWorkspace(true)
		veridianAllowAutomationSend(t, env, "auto-1", "client@gmail.com") // HasReplied => false, mais n'est meme pas consulte
		entry := transactionalTestEntry("tx")
		env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "t1").Return(nil)
		env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "t1").Return(nil)
		env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), false).Return(nil)
		env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
		env.worker.processEntry(ws, entry)
	})
	t.Run("une sequence supprimee ou non vivante arrete l'envoi", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		ws := transactionalTestWorkspace(true)
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		automations := mocks.NewMockAutomationRepository(ctrl)
		automations.EXPECT().GetByID(gomock.Any(), "ws-1", "auto-1").
			Return(&domain.Automation{ID: "auto-1", Status: domain.AutomationStatusPaused}, nil)
		env.worker.SetAutomationSendGuard(automations, nil, nil)
		entry := transactionalTestEntry("tx")
		// Fiche 62 : une sequence en pause garde sa ligne (reportee), elle n'est pas supprimee.
		env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "t1", gomock.Any()).Return(nil)
		env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		env.worker.processEntry(ws, entry)
	})
}

func TestVeridianReservedTransactionalProfileDoesNotApplyToTheNativeSingleton(t *testing.T) {
	// Pool vide et profil marketing == profil transactionnel : l'amont n'a qu'un profil
	// pour les deux, rien a separer. Aucun profil reserve.
	tx := veridianTestPoolIntegration("one", "hello@one.example")
	ws := &domain.Workspace{ID: "ws-1", Integrations: []domain.Integration{tx},
		Settings: domain.WorkspaceSettings{MarketingEmailProviderID: "one", TransactionalEmailProviderID: "one"}}
	assert.Empty(t, ws.VeridianReservedTransactionalProfileID())
	entry := veridianTestEntry("c1", "lead@gmail.com", domain.EmailQueuePayload{FromAddress: "hello@one.example"})
	entry.IntegrationID = "one"
	env := newVeridianThrottleTestEnv(t)
	cands := env.worker.veridianBuildFailoverCandidates(ws, entry, &ws.Integrations[0])
	require.Len(t, cands, 1)
	assert.Equal(t, "one", cands[0].IntegrationID, "le singleton partage reste un candidat commercial")
}
