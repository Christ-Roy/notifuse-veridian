package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingPipeline enveloppe le pipeline Veridian et consigne chaque etape dans le journal partage
// avec les appels amont (claim, SMTP, historique) : l'ordre de processEntry est ce que le test fige.
type recordingPipeline struct {
	inner SendPipeline
	trace *[]string
	w     *EmailQueueWorker // pour constater la prise du jeton du rate limiter (visible dans GetStats des qu'Wait a ete appele)
	noted bool
}

func (r *recordingPipeline) wrap(name string, f func(*SendContext) bool, sc *SendContext) bool {
	if r.w != nil && !r.noted && len(r.w.GetStats()) > 0 {
		r.noted = true
		*r.trace = append(*r.trace, "rate:Wait")
	}
	*r.trace = append(*r.trace, name+">")
	ok := f(sc)
	*r.trace = append(*r.trace, name+"<")
	return ok
}
func (r *recordingPipeline) Pre(sc *SendContext) bool    { return r.wrap("Pre", r.inner.Pre, sc) }
func (r *recordingPipeline) Select(sc *SendContext) bool { return r.wrap("Select", r.inner.Select, sc) }
func (r *recordingPipeline) PreRender(sc *SendContext) bool {
	return r.wrap("PreRender", r.inner.PreRender, sc)
}
func (r *recordingPipeline) PreSend(sc *SendContext) bool {
	return r.wrap("PreSend", r.inner.PreSend, sc)
}
func (r *recordingPipeline) Record(sc *SendContext, res SendResult) {
	if res.Err != nil {
		*r.trace = append(*r.trace, "Record(err)")
	} else {
		*r.trace = append(*r.trace, "Record(ok)")
	}
	r.inner.Record(sc, res)
}

func pipelineFixture(t *testing.T, sendErr error) (*EmailQueueWorker, *domain.Workspace, *domain.EmailQueueEntry, *[]string) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	workspace := &domain.Workspace{
		ID: "workspace-1",
		Integrations: []domain.Integration{{
			ID:            "integration-1",
			EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, RateLimitPerMinute: 100},
		}},
	}
	entry := &domain.EmailQueueEntry{
		ID: "entry-1", Status: domain.EmailQueueStatusPending, SourceType: domain.EmailQueueSourceBroadcast,
		SourceID: "broadcast-1", IntegrationID: "integration-1", ContactEmail: "test@example.com", MessageID: "msg-1",
		Payload: domain.EmailQueuePayload{
			FromAddress: "sender@example.com", FromName: "Sender", Subject: "Test Subject",
			HTMLContent: "<p>Hello</p>", RateLimitPerMinute: 100,
		},
		MaxAttempts: 3,
	}
	trace := []string{}
	mockMessageHistoryRepo.EXPECT().CountComplainedSinceForSenderDomain(gomock.Any(), "workspace-1", "example.com", gomock.Any()).Return(0, nil).AnyTimes()
	mockMessageHistoryRepo.EXPECT().CountSentSinceForSenderDomain(gomock.Any(), "workspace-1", "example.com", gomock.Any()).Return(0, nil).AnyTimes()
	mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "workspace-1", "entry-1").DoAndReturn(func(context.Context, string, string) error {
		trace = append(trace, "claim:MarkAsProcessing")
		return nil
	}).MaxTimes(1)
	mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).DoAndReturn(func(context.Context, domain.SendEmailProviderRequest, bool) error {
		trace = append(trace, "smtp:SendEmail")
		return sendErr
	}).MaxTimes(1)
	mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "workspace-1", gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, string, string, *domain.MessageHistory) error {
		trace = append(trace, "history:Upsert")
		return nil
	}).MaxTimes(1)
	if sendErr == nil {
		mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "workspace-1", "entry-1").DoAndReturn(func(context.Context, string, string) error {
			trace = append(trace, "queue:MarkAsSent")
			return nil
		}).MaxTimes(1)
	} else {
		mockQueueRepo.EXPECT().MarkAsFailed(gomock.Any(), "workspace-1", "entry-1", sendErr.Error(), gomock.Any()).DoAndReturn(func(context.Context, string, string, string, interface{}) error {
			trace = append(trace, "queue:MarkAsFailed")
			return nil
		}).MaxTimes(1)
	}

	w := NewEmailQueueWorker(mockQueueRepo, mockWorkspaceRepo, mockEmailService, mockMessageHistoryRepo, DefaultWorkerConfig(), mockLogger)
	w.ctx = context.Background()
	w.SetSendPipeline(&recordingPipeline{inner: &veridianSendPipeline{w: w}, trace: &trace, w: w})
	return w, workspace, entry, &trace
}

// Caracterisation (lot A, 29/10/2026) : l'ordre des etapes de processEntry et leur place par rapport a la prise
// de l'entree, au SMTP et a l'historique. Un changement d'ordre grille un domaine d'envoi (CLAUDE.md regle 4).
func TestProcessEntry_StageOrder_Success(t *testing.T) {
	w, ws, entry, trace := pipelineFixture(t, nil)
	w.processEntry(ws, entry)
	assert.Equal(t, []string{
		"Pre>", "Pre<",
		"Select>", "Select<",
		"PreRender>", "PreRender<",
		"rate:Wait",
		"PreSend>", "claim:MarkAsProcessing", "PreSend<",
		"smtp:SendEmail", "queue:MarkAsSent", "history:Upsert", "Record(ok)",
	}, *trace)
}

func TestProcessEntry_StageOrder_TransportFailure(t *testing.T) {
	w, ws, entry, trace := pipelineFixture(t, errors.New("SMTP connection failed"))
	w.processEntry(ws, entry)
	require.GreaterOrEqual(t, len(*trace), 12)
	assert.Equal(t, []string{
		"Pre>", "Pre<",
		"Select>", "Select<",
		"PreRender>", "PreRender<",
		"rate:Wait",
		"PreSend>", "claim:MarkAsProcessing", "PreSend<",
		"smtp:SendEmail", "Record(err)",
	}, (*trace)[:12], "Record(err) suit immediatement le transport, avant le traitement d'erreur")
}

// Une etape qui rend false arrete processEntry : rien de ce qui la suit ne s'execute.
func TestProcessEntry_StopHaltsTheChain(t *testing.T) {
	for _, stop := range []string{"Pre", "Select", "PreRender", "PreSend"} {
		t.Run(stop, func(t *testing.T) {
			w, ws, entry, trace := pipelineFixture(t, nil)
			rp := w.pipeline.(*recordingPipeline)
			rp.inner = stopAt{SendPipeline: rp.inner, stage: stop}
			w.processEntry(ws, entry)
			if stop != "PreSend" {
				assert.NotContains(t, *trace, "rate:Wait", "un envoi arrete avant PreSend ne consomme pas de jeton de debit")
			}
			assert.NotContains(t, *trace, "smtp:SendEmail")
			assert.NotContains(t, *trace, "Record(ok)")
			assert.Equal(t, stop+"<", (*trace)[len(*trace)-1], "la chaine s'arrete apres l'etape qui a rendu false")
		})
	}
}

type stopAt struct {
	SendPipeline
	stage string
}

func (s stopAt) Pre(sc *SendContext) bool {
	if s.stage == "Pre" {
		return false
	}
	return s.SendPipeline.Pre(sc)
}
func (s stopAt) Select(sc *SendContext) bool {
	if s.stage == "Select" {
		return false
	}
	return s.SendPipeline.Select(sc)
}
func (s stopAt) PreRender(sc *SendContext) bool {
	if s.stage == "PreRender" {
		return false
	}
	return s.SendPipeline.PreRender(sc)
}
func (s stopAt) PreSend(sc *SendContext) bool {
	if s.stage == "PreSend" {
		return false
	}
	return s.SendPipeline.PreSend(sc)
}

func TestSendPipeline_DefaultIsVeridianAndSetterReplaces(t *testing.T) {
	w := &EmailQueueWorker{}
	_, isDefault := w.sendPipeline().(*veridianSendPipeline)
	assert.True(t, isDefault, "sans pipeline pose, le pipeline Veridian s'applique (constructions litterales des tests)")
	custom := &recordingPipeline{}
	w.SetSendPipeline(custom)
	assert.Same(t, custom, w.sendPipeline())
	w.SetSendPipeline(nil)
	_, isDefault = w.sendPipeline().(*veridianSendPipeline)
	assert.True(t, isDefault)
}

// Pre passe AVANT la resolution de l'integration : une entree transactionnelle ou une relance sans J0 est reglee
// meme si son integration n'existe plus (ordre historique de processEntry).
func TestProcessEntry_PreRunsBeforeIntegrationLookup(t *testing.T) {
	w, ws, entry, trace := pipelineFixture(t, nil)
	entry.IntegrationID = "integration-inconnue"
	w.queueRepo.(*mocks.MockEmailQueueRepository).EXPECT().
		MarkAsProcessing(gomock.Any(), "workspace-1", "entry-1").Return(nil).AnyTimes()
	w.queueRepo.(*mocks.MockEmailQueueRepository).EXPECT().
		MarkAsFailed(gomock.Any(), "workspace-1", "entry-1", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	w.messageHistoryRepo.(*mocks.MockMessageHistoryRepository).EXPECT().
		Upsert(gomock.Any(), "workspace-1", gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	w.processEntry(ws, entry)
	require.GreaterOrEqual(t, len(*trace), 2)
	assert.Equal(t, []string{"Pre>", "Pre<"}, (*trace)[:2])
	assert.NotContains(t, *trace, "Select>", "integration inconnue : l'entree echoue avant Select")
}
