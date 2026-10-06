package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeQueuedEmailRenderer rend ce qu'on lui dit et compte ses appels.
type fakeQueuedEmailRenderer struct {
	out   *RenderedQueuedEmail
	err   error
	calls int
}

func (f *fakeQueuedEmailRenderer) RenderQueuedEmail(context.Context, *domain.Workspace, *domain.EmailQueueEntry) (*RenderedQueuedEmail, error) {
	f.calls++
	return f.out, f.err
}

func renderAtSendWorkspace() *domain.Workspace {
	return &domain.Workspace{
		ID: "ws-1",
		Integrations: []domain.Integration{{
			ID:   "int-1",
			Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind:               domain.EmailProviderKindSMTP,
				RateLimitPerMinute: 6000,
				Senders:            []domain.EmailSender{{ID: "s1", Email: "r@agence.example", Name: "R", IsDefault: true}},
			},
		}},
	}
}

// Une entree d'automation dont le contenu a ete fige a l'inscription (ANCIEN
// texte) doit partir avec le texte du modele courant.
func renderAtSendEntry() *domain.EmailQueueEntry {
	entry := veridianTestEntry("e1", "lead@gmail.com", domain.EmailQueuePayload{
		FromAddress: "r@agence.example",
		FromName:    "R",
		Subject:     "ANCIEN sujet",
		HTMLContent: "<p>ANCIEN html</p>",
		TextContent: "ANCIEN texte",
	})
	entry.SourceType = domain.EmailQueueSourceAutomation
	entry.SourceID = "auto-1"
	entry.TemplateID = "tpl-1"
	return entry
}

func TestRenderAtSend_QueuedMessageCarriesCurrentTemplate(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	veridianAllowAutomationSend(t, env, "auto-1", "lead@gmail.com")
	veridianExpectHealthyReputation(env, "ws-1", "agence.example")
	renderer := &fakeQueuedEmailRenderer{out: &RenderedQueuedEmail{
		Subject:         "NOUVEAU sujet",
		HTMLContent:     "<p>NOUVEAU html</p>",
		TextContent:     "NOUVEAU texte",
		PlainTextOnly:   true,
		ReplyTo:         "reponses@agence.example",
		TemplateVersion: 7,
	}}
	env.worker.SetQueuedEmailRenderer(renderer)

	entry := renderAtSendEntry()
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error {
			assert.Equal(t, int64(7), m.TemplateVersion, "message_history garde la version du modele reellement envoyee")
			return nil
		})

	var sent domain.SendEmailProviderRequest
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).
		DoAndReturn(func(_ context.Context, req domain.SendEmailProviderRequest, _ bool) error {
			sent = req
			return nil
		})

	env.worker.processEntry(renderAtSendWorkspace(), entry)

	require.Equal(t, 1, renderer.calls)
	assert.Equal(t, "NOUVEAU sujet", sent.Subject)
	assert.Equal(t, "<p>NOUVEAU html</p>", sent.Content)
	assert.Equal(t, "NOUVEAU texte", sent.TextContent)
	assert.True(t, sent.PlainTextOnly)
	assert.Equal(t, "reponses@agence.example", sent.EmailOptions.ReplyTo)
	// L'identite d'envoi n'est pas touchee par le rendu.
	assert.Equal(t, "r@agence.example", sent.FromAddress)
	assert.Equal(t, "int-1", sent.IntegrationID)
}

func TestRenderAtSend_PermanentFailureNeverSendsAndIsReadable(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	veridianAllowAutomationSend(t, env, "auto-1", "lead@gmail.com")
	veridianExpectHealthyReputation(env, "ws-1", "agence.example")
	env.worker.SetQueuedEmailRenderer(&fakeQueuedEmailRenderer{err: &RenderError{
		Reason: "modele tpl-1 illisible", Permanent: true, Err: errors.New("template not found"),
	}})

	var failedPermanent bool
	env.worker.SetCallbacks(nil, func(_ string, _ domain.EmailQueueSourceType, _ string, _ string, _ string, _ error, permanent bool) {
		failedPermanent = permanent
	})

	entry := renderAtSendEntry()
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockQueueRepo.EXPECT().Delete(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error {
			require.NotNil(t, m.FailedAt)
			assert.Nil(t, m.SentAt)
			require.NotNil(t, m.StatusInfo)
			assert.Contains(t, *m.StatusInfo, "render_at_send")
			assert.Contains(t, *m.StatusInfo, "modele tpl-1 illisible")
			return nil
		})

	env.worker.processEntry(renderAtSendWorkspace(), entry)
	assert.True(t, failedPermanent, "le contact d'automation doit etre resolu par le callback d'echec")
}

func TestRenderAtSend_TransientFailureRetriesWithoutSending(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	veridianAllowAutomationSend(t, env, "auto-1", "lead@gmail.com")
	veridianExpectHealthyReputation(env, "ws-1", "agence.example")
	env.worker.SetQueuedEmailRenderer(&fakeQueuedEmailRenderer{err: &RenderError{
		Reason: "contact illisible", Err: errors.New("connection reset"),
	}})

	entry := renderAtSendEntry()
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsFailed(gomock.Any(), "ws-1", "e1", gomock.Any(), gomock.Any()).Return(nil)
	env.mockQueueRepo.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

	env.worker.processEntry(renderAtSendWorkspace(), entry)
}

func TestRenderAtSend_EmptyRenderNeverSends(t *testing.T) {
	cases := map[string]*RenderedQueuedEmail{
		"sujet vide":      {Subject: "  ", HTMLContent: "<p>x</p>", TextContent: "x"},
		"corps vide":      {Subject: "S", HTMLContent: "", TextContent: " "},
		"texte seul vide": {Subject: "S", PlainTextOnly: true, HTMLContent: "<p>x</p>", TextContent: ""},
		"aucun contenu":   nil,
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			env := newVeridianThrottleTestEnv(t)
			veridianAllowAutomationSend(t, env, "auto-1", "lead@gmail.com")
			veridianExpectHealthyReputation(env, "ws-1", "agence.example")
			env.worker.SetQueuedEmailRenderer(&fakeQueuedEmailRenderer{out: out})

			entry := renderAtSendEntry()
			env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
			env.mockQueueRepo.EXPECT().Delete(gomock.Any(), "ws-1", "e1").Return(nil)
			env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)

			env.worker.processEntry(renderAtSendWorkspace(), entry)
			assert.Equal(t, "ANCIEN sujet", entry.Payload.Subject, "le contenu fige n'est pas remplace par un rendu vide")
		})
	}
}

// Un message de broadcast, ou sans modele, ne passe jamais par le renderer.
func TestRenderAtSend_OnlyAutomationEntriesWithTemplate(t *testing.T) {
	for name, mutate := range map[string]func(e *domain.EmailQueueEntry){
		"broadcast":     func(e *domain.EmailQueueEntry) { e.SourceType = domain.EmailQueueSourceBroadcast },
		"sans template": func(e *domain.EmailQueueEntry) { e.TemplateID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			env := newVeridianThrottleTestEnv(t)
			renderer := &fakeQueuedEmailRenderer{out: &RenderedQueuedEmail{Subject: "N", HTMLContent: "<p>n</p>"}}
			env.worker.SetQueuedEmailRenderer(renderer)

			entry := renderAtSendEntry()
			mutate(entry)
			if entry.SourceType == domain.EmailQueueSourceAutomation {
				veridianAllowAutomationSend(t, env, "auto-1", "lead@gmail.com")
				veridianExpectHealthyReputation(env, "ws-1", "agence.example")
			}
			assert.True(t, env.worker.veridianRenderAtSend(renderAtSendWorkspace(), entry))
			assert.Equal(t, 0, renderer.calls)
			assert.Equal(t, "ANCIEN sujet", entry.Payload.Subject)
		})
	}
}

// Sans renderer cable : comportement upstream/historique (envoi du payload).
func TestRenderAtSend_NoRendererKeepsFrozenPayload(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	entry := renderAtSendEntry()
	assert.True(t, env.worker.veridianRenderAtSend(renderAtSendWorkspace(), entry))
	assert.Equal(t, "ANCIEN texte", entry.Payload.TextContent)
}
