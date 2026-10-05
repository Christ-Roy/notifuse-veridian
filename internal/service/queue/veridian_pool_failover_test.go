package queue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// veridianAllowAutomationSend configure les trois garde-fous finaux
// d'automation (cf. veridian_automation_send_guard.go) en mode "tout est
// permis" : automation live, destinataire sans réponse, aucune liste à
// vérifier (ListID vide des deux côtés). Nécessaire dès qu'une entrée de test
// porte SourceType=Automation, sinon le worker les traite comme non
// configurés et échoue FERMÉ (comportement voulu en prod, pas ce que ces
// tests de failover de pool veulent exercer).
func veridianAllowAutomationSend(t *testing.T, env *veridianThrottleTestEnv, automationID, contactEmail string) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	automationRepo := mocks.NewMockAutomationRepository(ctrl)
	contactListRepo := mocks.NewMockContactListRepository(ctrl)
	replyRepo := mocks.NewMockVeridianContactReplyRepository(ctrl)
	automationRepo.EXPECT().
		GetByID(gomock.Any(), "ws-1", automationID).
		Return(&domain.Automation{ID: automationID, Status: domain.AutomationStatusLive}, nil).
		AnyTimes()
	replyRepo.EXPECT().HasReplied(gomock.Any(), "ws-1", contactEmail).Return(false, nil).AnyTimes()
	env.worker.SetAutomationSendGuard(automationRepo, contactListRepo, replyRepo)
}

// === Veridian — failover de pool à l'envoi (mission 2026-10-05) ===
//
// Reproduit puis corrige le défaut constaté en prod sur robertbrunon : un pool
// de deux intégrations SMTP, la première au plafond du jour, la seconde avec
// de la marge. Avant ce fork, l'entrée était reportée au lendemain sans
// jamais regarder la seconde intégration — le pool ne servait à rien.

// --- Helpers ---

func veridianTestPoolIntegration(id, senderEmail string) domain.Integration {
	verified := time.Now().Add(-48 * time.Hour)
	return domain.Integration{
		ID:   id,
		Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{
			Kind:                        domain.EmailProviderKindSMTP,
			RateLimitPerMinute:          6000,
			VeridianTransportVerifiedAt: &verified,
			Senders: []domain.EmailSender{
				{ID: "sender-" + id, Email: senderEmail, Name: "Veridian", IsDefault: true},
			},
		},
	}
}

func veridianTestPoolWorkspace(poolIDs []string, integrations []domain.Integration, classCaps map[string]int) *domain.Workspace {
	return &domain.Workspace{
		ID: "ws-1",
		Settings: domain.WorkspaceSettings{
			VeridianMarketingEmailProviderIDs: poolIDs,
			VeridianProviderClassDailyCap:     classCaps,
		},
		Integrations: integrations,
	}
}

// veridianExpectHealthyReputation arme les deux COUNT du fusible de
// réputation (veridian_reputation_gate.go) pour qu'un domaine émetteur donné
// soit toujours vu comme sain (zéro plainte, zéro volume → rien à geler).
// AnyTimes() : le nombre d'appels dépend de l'ordre d'essai des candidats,
// qui n'est pas ce que ces tests veulent prouver.
func veridianExpectHealthyReputation(env *veridianThrottleTestEnv, workspaceID, senderDomain string) {
	env.mockMessageHistoryRepo.EXPECT().
		CountComplainedSinceForSenderDomain(gomock.Any(), workspaceID, senderDomain, gomock.Any()).
		Return(0, nil).AnyTimes()
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), workspaceID, senderDomain, gomock.Any()).
		Return(0, nil).AnyTimes()
}

// newVeridianPoolQuotaRepo construit le repo de test qui satisfait à la fois
// MessageHistoryRepository (délégué au mock gomock, pour les gates coarse) et
// VeridianDailyQuotaRepository (réserve systématiquement, comme un pool avec
// de la marge réelle). Réutilise quotaTestRepository, déjà défini dans
// veridian_daily_quota_test.go (même package).
func newVeridianPoolQuotaRepo(env *veridianThrottleTestEnv) *quotaTestRepository {
	return &quotaTestRepository{
		MessageHistoryRepository: env.mockMessageHistoryRepo,
		outcomes: map[string]quotaTestOutcome{
			domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
		},
	}
}

// === 1. Reproduction + correctif du défaut du 05/10 ===

// TestEmailQueueWorker_ProcessEntry_PoolFailoverWhenAssignedIntegrationCapped
// est LE test qui reproduit le défaut : sans le correctif de ce fork, nord
// est au plafond, l'entrée est reportée (SetNextRetry, aucun SendEmail) même
// si relai a de la marge. Avec le correctif, l'entrée part par relai
// aujourd'hui : From, IntegrationID et ProviderKind sont réécrits sur relai.
func TestEmailQueueWorker_ProcessEntry_PoolFailoverWhenAssignedIntegrationCapped(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		map[string]int{"google": 1},
	)

	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})
	entry.IntegrationID = "nord"
	entry.Payload.FromAddress = "hello@nord-propre.example"
	entry.Payload.FromName = "Veridian"

	veridianExpectHealthyReputation(env, "ws-1", "nord-propre.example")
	veridianExpectHealthyReputation(env, "ws-1", "relai-agence.example")

	// nord a déjà consommé son plafond du jour sur la classe google ; relai a
	// toute sa marge.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "nord-propre.example", gomock.Any()).
		Return(1, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "relai-agence.example", gomock.Any()).
		Return(0, nil)

	env.worker.messageHistoryRepo = newVeridianPoolQuotaRepo(env)

	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

	var sentReq domain.SendEmailProviderRequest
	env.mockEmailService.EXPECT().
		SendEmail(gomock.Any(), gomock.Any(), true).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error {
			sentReq = req
			return nil
		})

	// Aucun report : le bug reproduit ici est exactement un appel à
	// SetNextRetry à la place de l'envoi via relai.
	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(workspace, entry)

	require.Equal(t, "relai", sentReq.IntegrationID)
	assert.Equal(t, "hello@relai-agence.example", sentReq.FromAddress)
	assert.Equal(t, "relai", entry.IntegrationID, "l'entrée doit être réécrite sur l'intégration qui a réellement envoyé")
	assert.Equal(t, "hello@relai-agence.example", entry.Payload.FromAddress)
	assert.Equal(t, domain.EmailProviderKindSMTP, entry.ProviderKind)
}

// TestEmailQueueWorker_ProcessEntry_PoolFailover_NoneHaveRoom_Reschedules
// prouve le symétrique : si AUCUN membre du pool n'a de marge, l'entrée est
// reportée (pas de SendEmail), exactement comme avant ce fork pour une
// intégration seule.
func TestEmailQueueWorker_ProcessEntry_PoolFailover_NoneHaveRoom_Reschedules(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		map[string]int{"google": 1},
	)

	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})
	entry.IntegrationID = "nord"
	entry.Payload.FromAddress = "hello@nord-propre.example"

	veridianExpectHealthyReputation(env, "ws-1", "nord-propre.example")
	veridianExpectHealthyReputation(env, "ws-1", "relai-agence.example")

	// Les deux infras sont au plafond.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "nord-propre.example", gomock.Any()).
		Return(1, nil)
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "relai-agence.example", gomock.Any()).
		Return(1, nil)

	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "e1", gomock.Any()).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Times(0)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	env.worker.processEntry(workspace, entry)

	// Non-réécrit : l'entrée reste attribuée à son intégration d'origine pour
	// le prochain essai (relecture fraîche de la ligne au prochain tick).
	assert.Equal(t, "nord", entry.IntegrationID)
}

// === 2. Répartition : deux membres à 300/jour, 600 messages dus ===

// TestEmailQueueWorker_ProcessEntry_PoolFailover_SplitsLoadAcrossBothMembers
// exerce 600 entrées assignées à nord avec un plafond de classe de 300 par
// infra. Un compteur en mémoire (poolCapTestRepo) tient lieu de
// message_history : les 300 premières partent par nord, puis nord atteint
// son plafond et les 300 suivantes basculent sur relai. Les deux envoient.
type poolCapTestRepo struct {
	domain.MessageHistoryRepository
	mu     chan struct{} // utilisé comme mutex trivial (buffered chan de taille 1)
	sentBy map[string]int
	cap    int
}

func newPoolCapTestRepo(cap int) *poolCapTestRepo {
	r := &poolCapTestRepo{mu: make(chan struct{}, 1), sentBy: map[string]int{}, cap: cap}
	r.mu <- struct{}{}
	return r
}

func (r *poolCapTestRepo) lock()   { <-r.mu }
func (r *poolCapTestRepo) unlock() { r.mu <- struct{}{} }

func (r *poolCapTestRepo) CountSentSinceForClassAndSenderDomain(_ context.Context, _ string, _ string, senderDomain string, _ time.Time) (int, error) {
	r.lock()
	defer r.unlock()
	return r.sentBy[senderDomain], nil
}
func (r *poolCapTestRepo) CountComplainedSinceForSenderDomain(context.Context, string, string, time.Time) (int, error) {
	return 0, nil
}
func (r *poolCapTestRepo) CountSentSinceForSenderDomain(_ context.Context, _ string, senderDomain string, since time.Time) (int, error) {
	// Appelé aussi par le fusible de réputation (fenêtre 7j) : réutilise le
	// même compteur, toujours sous le seuil de bounce (aucun bounce simulé).
	r.lock()
	defer r.unlock()
	return r.sentBy[senderDomain], nil
}
func (r *poolCapTestRepo) CountHardBouncedSinceForSenderDomain(context.Context, string, string, time.Time) (int, error) {
	return 0, nil
}
func (r *poolCapTestRepo) ListUnclassifiedSuccessfulMessagesSince(context.Context, string, time.Time) ([]domain.VeridianUnclassifiedSuccessfulMessage, error) {
	return nil, nil
}
func (r *poolCapTestRepo) SetMessageProviderClassIfEmpty(context.Context, string, string, string) error {
	return nil
}
func (r *poolCapTestRepo) ReserveDailyQuota(_ context.Context, _ string, reservation domain.VeridianDailyQuotaReservation) (domain.VeridianDailyQuotaReservationResult, error) {
	r.lock()
	defer r.unlock()
	domainKey := reservation.Key.SenderDomain
	used := r.sentBy[domainKey]
	if used >= reservation.Cap {
		return domain.VeridianDailyQuotaReservationResult{Reserved: false, Used: used}, nil
	}
	r.sentBy[domainKey] = used + 1
	return domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: used + 1}, nil
}
func (r *poolCapTestRepo) ReleaseDailyQuota(_ context.Context, _, _, _ string) error { return nil }
func (r *poolCapTestRepo) Upsert(context.Context, string, string, *domain.MessageHistory) error {
	return nil
}

func TestEmailQueueWorker_ProcessEntry_PoolFailover_SplitsLoadAcrossBothMembers(t *testing.T) {
	const perInfraCap = 300
	const totalDue = 600

	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		map[string]int{"google": perInfraCap},
	)

	env := newVeridianThrottleTestEnv(t)
	env.worker.messageHistoryRepo = newPoolCapTestRepo(perInfraCap)

	// Expectations posées UNE SEULE FOIS (gomock.Any() sur l'id) : les 600
	// itérations partagent les mêmes attentes, seul le compteur ci-dessous
	// distingue qui a réellement envoyé quoi.
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", gomock.Any()).Return(nil).AnyTimes()
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", gomock.Any()).Return(nil).AnyTimes()
	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	env.mockQueueRepo.EXPECT().SetNextRetryAndRefundAttempt(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	sentCount := map[string]int{}
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).DoAndReturn(
		func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error {
			sentCount[req.IntegrationID]++
			return nil
		}).AnyTimes()

	for i := 0; i < totalDue; i++ {
		id := fmt.Sprintf("e%d", i)
		entry := veridianTestEntry(id, "lead@gmail.com", domain.EmailQueuePayload{})
		entry.IntegrationID = "nord"
		entry.Payload.FromAddress = "hello@nord-propre.example"
		entry.ID = id
		entry.MessageID = "msg-" + id

		env.worker.processEntry(workspace, entry)
	}

	assert.Equal(t, perInfraCap, sentCount["nord"], "nord doit envoyer jusqu'à son plafond")
	assert.Equal(t, perInfraCap, sentCount["relai"], "relai doit absorber tout le reste jusqu'à SON plafond")
	assert.Equal(t, totalDue, sentCount["nord"]+sentCount["relai"], "les 600 messages dus partent, répartis sur les deux relais")
}

// === 3. Continuité de séquence : relance garde le même expéditeur que J0 ===

func veridianTestAutomationEntry(id, automationID, contactEmail, from string) *domain.EmailQueueEntry {
	e := veridianTestEntry(id, contactEmail, domain.EmailQueuePayload{FromAddress: from})
	e.SourceType = domain.EmailQueueSourceAutomation
	e.SourceID = automationID
	e.MessageID = "msg-" + id
	e.IntegrationID = "nord" // veridianTestEntry défaut sur "int-1", absent du pool nord/relai de ces tests
	return e
}

func veridianTestSequenceHistory(automationID, contactEmail, integrationID, senderEmail string, sentAt time.Time) []*domain.MessageHistory {
	aid := automationID
	return []*domain.MessageHistory{
		{
			ID:                  "j0-msg",
			ContactEmail:        contactEmail,
			AutomationID:        &aid,
			SentAt:              &sentAt,
			VeridianProfileID:   integrationID,
			VeridianSenderEmail: senderEmail,
		},
	}
}

// TestEmailQueueWorker_SequenceContinuity_KeepsAnchorWhenAvailable_EvenIfCappedToday
// : J0 est parti par nord. La relance (J+4) est aussi assignée à nord, qui
// est capped AUJOURD'HUI, mais nord a encore envoyé dans les dernières 48h
// (disponible). La relance NE BASCULE PAS sur relai : elle est reportée.
func TestEmailQueueWorker_SequenceContinuity_KeepsAnchorWhenAvailable_EvenIfCappedToday(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		map[string]int{"google": 1},
	)

	entry := veridianTestAutomationEntry("relance1", "automation-1", "lead@gmail.com", "hello@nord-propre.example")
	veridianAllowAutomationSend(t, env, "automation-1", "lead@gmail.com")

	j0SentAt := time.Now().Add(-4 * 24 * time.Hour)
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@gmail.com", gomock.Any(), 0).
		Return(veridianTestSequenceHistory("automation-1", "lead@gmail.com", "nord", "hello@nord-propre.example", j0SentAt), 1, nil)

	// L'ancre (nord) a encore envoyé il y a moins de 48h → disponible.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "nord-propre.example", gomock.Any()).
		Return(1, nil)

	veridianExpectHealthyReputation(env, "ws-1", "nord-propre.example")

	// nord est capped aujourd'hui sur la classe google.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForClassAndSenderDomain(gomock.Any(), "ws-1", "google", "nord-propre.example", gomock.Any()).
		Return(1, nil)

	env.mockQueueRepo.EXPECT().SetNextRetry(gomock.Any(), "ws-1", "relance1", gomock.Any()).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Times(0)

	env.worker.processEntry(workspace, entry)

	assert.Equal(t, "nord", entry.IntegrationID, "la relance reste sur l'expéditeur de J0, même capped aujourd'hui")
}

// TestEmailQueueWorker_SequenceContinuity_SwitchesWhenAnchorUnavailable48h : J0
// est parti par nord, qui n'a RIEN envoyé depuis plus de 48h (indisponible).
// La relance bascule alors sur relai, qui a de la marge.
func TestEmailQueueWorker_SequenceContinuity_SwitchesWhenAnchorUnavailable48h(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		map[string]int{}, // aucun plafond de classe : seule l'indisponibilité de l'ancre est en jeu
	)

	entry := veridianTestAutomationEntry("relance1", "automation-1", "lead@gmail.com", "hello@nord-propre.example")
	veridianAllowAutomationSend(t, env, "automation-1", "lead@gmail.com")

	j0SentAt := time.Now().Add(-10 * 24 * time.Hour)
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@gmail.com", gomock.Any(), 0).
		Return(veridianTestSequenceHistory("automation-1", "lead@gmail.com", "nord", "hello@nord-propre.example", j0SentAt), 1, nil)

	// nord n'a RIEN envoyé depuis plus de 48h → indisponible.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForSenderDomain(gomock.Any(), "ws-1", "nord-propre.example", gomock.Any()).
		Return(0, nil)

	veridianExpectHealthyReputation(env, "ws-1", "relai-agence.example")
	// nord reste essayé en dernier recours (toujours joignable techniquement,
	// juste déprioritisé) : le fusible de réputation doit pouvoir être
	// évalué si relai échouait, mais ici relai gagne avant qu'on l'atteigne.

	repo := newVeridianPoolQuotaRepo(env)
	env.worker.messageHistoryRepo = repo

	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "relance1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "relance1").Return(nil)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

	var sentReq domain.SendEmailProviderRequest
	env.mockEmailService.EXPECT().
		SendEmail(gomock.Any(), gomock.Any(), true).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error {
			sentReq = req
			return nil
		})

	env.worker.processEntry(workspace, entry)

	assert.Equal(t, "relai", sentReq.IntegrationID, "l'ancre est indisponible depuis plus de 48h : la relance peut changer de domaine")
	assert.Equal(t, "relai", entry.IntegrationID)
}

// === 4. Fonctions pures : ordre des candidats (zéro I/O) ===

func TestVeridianFailoverCandidateIDs_AnchorAvailable_StaysAloneNoFallback(t *testing.T) {
	got := veridianFailoverCandidateIDs([]string{"nord", "relai"}, "nord", "nord", true, true)
	assert.Equal(t, []string{"nord"}, got)
}

func TestVeridianFailoverCandidateIDs_AnchorUnavailable_OpensFailoverAnchorLast(t *testing.T) {
	got := veridianFailoverCandidateIDs([]string{"nord", "relai"}, "nord", "nord", true, false)
	assert.Equal(t, []string{"relai", "nord"}, got)
}

func TestVeridianFailoverCandidateIDs_NoAnchor_AssignedFirstThenPool(t *testing.T) {
	got := veridianFailoverCandidateIDs([]string{"nord", "relai", "tiers"}, "relai", "", false, false)
	assert.Equal(t, []string{"relai", "nord", "tiers"}, got)
}

func TestVeridianFailoverCandidateIDs_NoAssignedID_PoolOrderPreserved(t *testing.T) {
	got := veridianFailoverCandidateIDs([]string{"nord", "relai"}, "", "", false, false)
	assert.Equal(t, []string{"nord", "relai"}, got)
}

// === 5. Exclusion de classe préservée : repli si un seul candidat exclut ===

func TestEmailQueueWorker_ProcessEntry_ExcludedOnOneCandidateFallsOverToOther(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	nord.EmailProvider.VeridianExcludedProviderClasses = []string{"google"}
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		nil,
	)

	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})
	entry.IntegrationID = "nord"
	entry.Payload.FromAddress = "hello@nord-propre.example"

	veridianExpectHealthyReputation(env, "ws-1", "relai-agence.example")
	repo := newVeridianPoolQuotaRepo(env)
	env.worker.messageHistoryRepo = repo

	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

	var sentReq domain.SendEmailProviderRequest
	env.mockEmailService.EXPECT().
		SendEmail(gomock.Any(), gomock.Any(), true).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error {
			sentReq = req
			return nil
		})

	env.worker.processEntry(workspace, entry)

	assert.Equal(t, "relai", sentReq.IntegrationID, "nord exclut google, relai ne l'exclut pas : relai doit envoyer")
}

func TestEmailQueueWorker_ProcessEntry_ExcludedOnEveryCandidateFailsPermanently(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	nord := veridianTestPoolIntegration("nord", "hello@nord-propre.example")
	nord.EmailProvider.VeridianExcludedProviderClasses = []string{"google"}
	relai := veridianTestPoolIntegration("relai", "hello@relai-agence.example")
	relai.EmailProvider.VeridianExcludedProviderClasses = []string{"google"}
	workspace := veridianTestPoolWorkspace(
		[]string{"nord", "relai"},
		[]domain.Integration{nord, relai},
		nil,
	)

	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{})
	entry.IntegrationID = "nord"
	entry.Payload.FromAddress = "hello@nord-propre.example"

	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.mockQueueRepo.EXPECT().Delete(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Times(0)

	env.worker.processEntry(workspace, entry)
}
