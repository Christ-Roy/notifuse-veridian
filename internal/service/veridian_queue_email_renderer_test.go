package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queueRendererEnv struct {
	renderer       *VeridianQueueEmailRenderer
	templateRepo   *mocks.MockTemplateRepository
	listRepo       *mocks.MockListRepository
	contactRepo    *mocks.MockContactRepository
	automationRepo *mocks.MockAutomationRepository
	workspace      *domain.Workspace
}

func newQueueRendererEnv(t *testing.T) *queueRendererEnv {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	env := &queueRendererEnv{
		templateRepo:   mocks.NewMockTemplateRepository(ctrl),
		listRepo:       mocks.NewMockListRepository(ctrl),
		contactRepo:    mocks.NewMockContactRepository(ctrl),
		automationRepo: mocks.NewMockAutomationRepository(ctrl),
		workspace:      createTestWorkspaceWithEmailProvider(),
	}
	env.renderer = NewVeridianQueueEmailRenderer(env.templateRepo, env.listRepo, env.contactRepo, env.automationRepo, "https://api.example.com")
	return env
}

func (e *queueRendererEnv) entry() *domain.EmailQueueEntry {
	return &domain.EmailQueueEntry{
		ID: "q1", SourceType: domain.EmailQueueSourceAutomation, SourceID: "auto1",
		TemplateID: "tpl123", ContactEmail: "recipient@example.com", MessageID: "ws1_msg1",
		Payload: domain.EmailQueuePayload{ListID: "list1"},
	}
}

func (e *queueRendererEnv) expectLookups(template *domain.Template) {
	e.automationRepo.EXPECT().GetByID(gomock.Any(), "ws1", "auto1").
		Return(&domain.Automation{ID: "auto1", Name: "Test Automation", ListID: "list1"}, nil)
	e.templateRepo.EXPECT().GetTemplateByID(gomock.Any(), "ws1", "tpl123", int64(0)).Return(template, nil)
	e.contactRepo.EXPECT().GetContactByEmail(gomock.Any(), "ws1", "recipient@example.com").
		Return(&domain.Contact{Email: "recipient@example.com"}, nil)
	e.listRepo.EXPECT().GetListByID(gomock.Any(), "ws1", "list1").Return(&domain.List{ID: "list1", Name: "Test List"}, nil)
}

// Preuve centrale : un message est mis en file avec le modele V1, le modele est
// corrige, le rendu au depilage porte le texte V2 (et pas celui de la file).
func TestVeridianQueueEmailRenderer_FollowsTemplateEditedAfterEnqueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// 1. Enqueue via le vrai noeud email, modele V1.
	enqueueQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	enqueueTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	enqueueWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	enqueueListRepo := mocks.NewMockListRepository(ctrl)
	executor := NewEmailNodeExecutor(enqueueQueueRepo, enqueueTemplateRepo, enqueueWorkspaceRepo, enqueueListRepo,
		mocks.NewMockContactListRepository(ctrl), "https://api.example.com", setupMockLoggerForNodeExecutor(ctrl))

	workspace := createTestWorkspaceWithEmailProvider()
	v1 := createTestTemplate()
	enqueueWorkspaceRepo.EXPECT().GetByID(gomock.Any(), "ws1").Return(workspace, nil)
	enqueueTemplateRepo.EXPECT().GetTemplateByID(gomock.Any(), "ws1", "tpl123", int64(0)).Return(v1, nil)
	enqueueListRepo.EXPECT().GetListByID(gomock.Any(), "ws1", "list1").Return(&domain.List{ID: "list1", Name: "Test List"}, nil)
	var queued *domain.EmailQueueEntry
	enqueueQueueRepo.EXPECT().Enqueue(gomock.Any(), "ws1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, entries []*domain.EmailQueueEntry) error {
			queued = entries[0]
			return nil
		})
	_, err := executor.Execute(context.Background(), NodeExecutionParams{
		WorkspaceID: "ws1",
		Node:        &domain.AutomationNode{ID: "n1", Type: domain.NodeTypeEmail, Config: map[string]interface{}{"template_id": "tpl123"}},
		Contact:     &domain.ContactAutomation{ID: "ca1", ContactEmail: "recipient@example.com"},
		ContactData: &domain.Contact{Email: "recipient@example.com"},
		Automation:  &domain.Automation{ID: "auto1", Name: "Test Automation", ListID: "list1"},
	})
	require.NoError(t, err)
	require.NotNil(t, queued)
	assert.Equal(t, "Test Subject", queued.Payload.Subject)
	assert.Equal(t, "Bonjour recipient@example.com", queued.Payload.TextContent)

	// 2. Le modele est corrige pendant que le message attend en file.
	v2 := createTestTemplate()
	v2.Version = 2
	newText := "Bonsoir {{ contact.email }}, version corrigee"
	v2.Email.Text = &newText
	v2.Email.Subject = "Sujet corrige"

	// 3. Rendu au depilage.
	env := newQueueRendererEnv(t)
	env.expectLookups(v2)
	out, err := env.renderer.RenderQueuedEmail(context.Background(), env.workspace, queued)
	require.NoError(t, err)

	assert.Equal(t, "Sujet corrige", out.Subject)
	assert.Equal(t, "Bonsoir recipient@example.com, version corrigee", out.TextContent)
	assert.NotEqual(t, queued.Payload.TextContent, out.TextContent, "le texte fige a l'enqueue ne doit plus partir")
	assert.NotEmpty(t, out.HTMLContent)
	assert.True(t, out.PlainTextOnly)
	assert.Equal(t, 2, out.TemplateVersion)
}

func TestVeridianQueueEmailRenderer_ErrorClassification(t *testing.T) {
	type tc struct {
		name      string
		arrange   func(e *queueRendererEnv)
		permanent bool
	}
	cases := []tc{
		{"modele supprime", func(e *queueRendererEnv) {
			e.automationRepo.EXPECT().GetByID(gomock.Any(), "ws1", "auto1").Return(&domain.Automation{ID: "auto1", ListID: "list1"}, nil)
			e.templateRepo.EXPECT().GetTemplateByID(gomock.Any(), "ws1", "tpl123", int64(0)).
				Return(nil, &domain.ErrTemplateNotFound{Message: "template not found"})
		}, true},
		{"lecture modele en panne", func(e *queueRendererEnv) {
			e.automationRepo.EXPECT().GetByID(gomock.Any(), "ws1", "auto1").Return(&domain.Automation{ID: "auto1", ListID: "list1"}, nil)
			e.templateRepo.EXPECT().GetTemplateByID(gomock.Any(), "ws1", "tpl123", int64(0)).Return(nil, errors.New("connection reset"))
		}, false},
		{"contact supprime", func(e *queueRendererEnv) {
			e.automationRepo.EXPECT().GetByID(gomock.Any(), "ws1", "auto1").Return(&domain.Automation{ID: "auto1", ListID: "list1"}, nil)
			e.templateRepo.EXPECT().GetTemplateByID(gomock.Any(), "ws1", "tpl123", int64(0)).Return(createTestTemplate(), nil)
			e.contactRepo.EXPECT().GetContactByEmail(gomock.Any(), "ws1", "recipient@example.com").Return(nil, domain.ErrContactNotFound)
		}, true},
		{"liquid invalide", func(e *queueRendererEnv) {
			bad := createTestTemplate()
			broken := "{% if %}"
			bad.Email.Subject = broken
			e.expectLookups(bad)
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newQueueRendererEnv(t)
			c.arrange(env)
			out, err := env.renderer.RenderQueuedEmail(context.Background(), env.workspace, env.entry())
			require.Error(t, err)
			assert.Nil(t, out)
			var renderErr *queue.RenderError
			require.True(t, errors.As(err, &renderErr))
			assert.Equal(t, c.permanent, renderErr.Permanent)
			assert.Contains(t, err.Error(), "render_at_send")
		})
	}
}
