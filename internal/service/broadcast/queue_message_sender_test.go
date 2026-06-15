package broadcast

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	bmocks "github.com/Notifuse/notifuse/internal/service/broadcast/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewQueueMessageSender(t *testing.T) {
	t.Run("creates sender with all dependencies", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		require.NotNil(t, sender)
		assert.Implements(t, (*MessageSender)(nil), sender)
	})

	t.Run("uses default config when nil provided", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil, // nil config
			"https://api.example.com",
		)

		require.NotNil(t, sender)
	})

	t.Run("initializes circuit breaker when enabled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		config := &Config{
			EnableCircuitBreaker:    true,
			CircuitBreakerThreshold: 5,
			CircuitBreakerCooldown:  30 * time.Second,
		}

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			config,
			"https://api.example.com",
		)

		require.NotNil(t, sender)
	})

	t.Run("creates sender with data feed fetcher", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockDataFeedFetcher := bmocks.NewMockDataFeedFetcher(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			mockDataFeedFetcher,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		require.NotNil(t, sender)
		assert.Implements(t, (*MessageSender)(nil), sender)
		qms := sender.(*queueMessageSender)
		assert.NotNil(t, qms.dataFeedFetcher)
	})
}

// Helper functions for creating test data
func createQueueTestTextBlock(id, textContent string) notifuse_mjml.EmailBlock {
	content := textContent
	base := notifuse_mjml.NewBaseBlock(id, notifuse_mjml.MJMLComponentMjText)
	base.Content = &content
	return &notifuse_mjml.MJTextBlock{BaseBlock: base}
}

func createQueueValidTestTree(textBlock notifuse_mjml.EmailBlock) notifuse_mjml.EmailBlock {
	columnBase := notifuse_mjml.NewBaseBlock("col1", notifuse_mjml.MJMLComponentMjColumn)
	columnBase.Children = []notifuse_mjml.EmailBlock{textBlock}
	columnBlock := &notifuse_mjml.MJColumnBlock{BaseBlock: columnBase}

	sectionBase := notifuse_mjml.NewBaseBlock("sec1", notifuse_mjml.MJMLComponentMjSection)
	sectionBase.Children = []notifuse_mjml.EmailBlock{columnBlock}
	sectionBlock := &notifuse_mjml.MJSectionBlock{BaseBlock: sectionBase}

	bodyBase := notifuse_mjml.NewBaseBlock("body1", notifuse_mjml.MJMLComponentMjBody)
	bodyBase.Children = []notifuse_mjml.EmailBlock{sectionBlock}
	bodyBlock := &notifuse_mjml.MJBodyBlock{BaseBlock: bodyBase}

	rootBase := notifuse_mjml.NewBaseBlock("root", notifuse_mjml.MJMLComponentMjml)
	rootBase.Children = []notifuse_mjml.EmailBlock{bodyBlock}
	return &notifuse_mjml.MJMLBlock{BaseBlock: rootBase}
}

// Veridian fork — couvre le setter DI du dédupliqueur anti-hash. Sans dedup
// injecté (état par défaut), buildQueueEntry NE pose AUCUN hash (non-régression
// upstream stricte) ; une fois injecté, le champ est bien câblé.
func TestQueueMessageSender_SetVeridianContentDedup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	sender := NewQueueMessageSender(
		mocks.NewMockEmailQueueRepository(ctrl),
		mocks.NewMockBroadcastRepository(ctrl),
		mocks.NewMockMessageHistoryRepository(ctrl),
		mocks.NewMockTemplateRepository(ctrl),
		nil,
		pkgmocks.NewMockLogger(ctrl),
		nil,
		"https://api.example.com",
	)
	qms := sender.(*queueMessageSender)

	// État par défaut : aucun dedup → anti-hash inactif (non-régression).
	assert.Nil(t, qms.veridianContentDedup)

	// Injection : le setter câble le dédupliqueur.
	d := newVeridianContentDedup(mocks.NewMockMessageHistoryRepository(ctrl), qms.logger)
	qms.SetVeridianContentDedup(d)
	assert.Same(t, d, qms.veridianContentDedup)

	// Réinjection nil : remet l'anti-hash inactif (idempotent).
	qms.SetVeridianContentDedup(nil)
	assert.Nil(t, qms.veridianContentDedup)
}

func TestQueueMessageSender_SendToRecipient(t *testing.T) {
	t.Run("successfully enqueues single email", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		// Setup logger expectations
		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
			SMTP:    &domain.SMTPSettings{Host: "smtp.example.com", Port: 587},
		}

		broadcast := &domain.Broadcast{
			ID:          "broadcast-1",
			WorkspaceID: "workspace-1",
			Name:        "Test Broadcast",
			UTMParameters: &domain.UTMParameters{
				Source:   "test",
				Medium:   "email",
				Campaign: "campaign-1",
			},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test Subject",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello World")),
			},
		}

		// Expect enqueue call
		mockQueueRepo.EXPECT().Enqueue(
			gomock.Any(),
			"workspace-1",
			gomock.Any(),
		).Return(nil)

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		err := sender.SendToRecipient(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-1",
			"recipient@example.com",
			template,
			map[string]interface{}{"contact": map[string]interface{}{"email": "recipient@example.com"}},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"", "",
		)

		assert.NoError(t, err)
	})

	t.Run("returns error on enqueue failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			UTMParameters: &domain.UTMParameters{},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test Subject",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(errors.New("database error"))

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		err := sender.SendToRecipient(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-1",
			"recipient@example.com",
			template,
			map[string]interface{}{},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"", "",
		)

		assert.Error(t, err)
	})

	t.Run("returns error when no sender configured", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{}, // No senders
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			UTMParameters: &domain.UTMParameters{},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         "non-existent-sender",
				Subject:          "Test Subject",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		err := sender.SendToRecipient(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-1",
			"recipient@example.com",
			template,
			map[string]interface{}{},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"", "",
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no sender configured")
	})
}

func TestQueueMessageSender_SendBatch(t *testing.T) {
	t.Run("successfully enqueues batch of emails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			Name:          "Test Broadcast",
			UTMParameters: &domain.UTMParameters{Source: "test", Medium: "email"},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test Subject",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		recipients := []*domain.ContactWithList{
			{
				Contact: &domain.Contact{Email: "user1@example.com"},
				ListID:  "list-1",
			},
			{
				Contact: &domain.Contact{Email: "user2@example.com"},
				ListID:  "list-1",
			},
		}

		mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
			Return(broadcast, nil)

		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
			DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
				assert.Len(t, entries, 2)
				return nil
			})

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		sent, failed, err := sender.SendBatch(
			context.Background(),
			"workspace-1",
			"integration-1",
			"secret-key",
			"https://api.example.com",
			"",
			true,
			"broadcast-1",
			recipients,
			map[string]*domain.Template{"template-1": template},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"",
		)

		assert.NoError(t, err)
		assert.Equal(t, 2, sent)
		assert.Equal(t, 0, failed)
	})

	t.Run("handles empty recipients", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		sent, failed, err := sender.SendBatch(
			context.Background(),
			"workspace-1",
			"integration-1",
			"secret-key",
			"https://api.example.com",
			"",
			true,
			"broadcast-1",
			[]*domain.ContactWithList{}, // Empty
			nil,
			nil,
			time.Now().Add(5*time.Minute),
			"",
		)

		assert.NoError(t, err)
		assert.Equal(t, 0, sent)
		assert.Equal(t, 0, failed)
	})

	t.Run("handles enqueue failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
		mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			UTMParameters: &domain.UTMParameters{},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test Subject",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		recipients := []*domain.ContactWithList{
			{Contact: &domain.Contact{Email: "user1@example.com"}},
		}

		mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
			Return(broadcast, nil)

		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
			Return(errors.New("database error"))

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		sent, failed, err := sender.SendBatch(
			context.Background(),
			"workspace-1",
			"integration-1",
			"secret-key",
			"https://api.example.com",
			"",
			true,
			"broadcast-1",
			recipients,
			map[string]*domain.Template{"template-1": template},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"",
		)

		assert.Error(t, err)
		assert.Equal(t, 0, sent)
		assert.Equal(t, 1, failed)
	})

	t.Run("populates system variables via BuildTemplateData", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			Name:          "Weekly Newsletter",
			UTMParameters: &domain.UTMParameters{Source: "newsletter", Medium: "email"},
		}

		// Template with system variable placeholders that should be rendered
		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test Subject",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "<a href=\"{{ unsubscribe_url }}\">Unsubscribe</a> | <a href=\"{{ notification_center_url }}\">Preferences</a>")),
			},
		}

		// Recipients with full list info for BuildTemplateData
		recipients := []*domain.ContactWithList{
			{
				Contact:  &domain.Contact{Email: "user@example.com"},
				ListID:   "list-123",
				ListName: "Subscribers",
			},
		}

		mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
			Return(broadcast, nil)

		// Verify enqueued entry contains rendered system URLs
		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
			DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
				require.Len(t, entries, 1)
				entry := entries[0]

				// System variables should be rendered in HTML (not raw Liquid)
				// URLs are now inside encrypted /r/ tracking tokens
				assert.Contains(t, entry.Payload.HTMLContent, "/r/",
					"system URLs should be rendered as encrypted tracking redirects")
				// Tracking pixel should be present with encrypted /t/ path and table wrapper
				assert.Contains(t, entry.Payload.HTMLContent, "/t/",
					"tracking pixel should use encrypted /t/ path")
				assert.Contains(t, entry.Payload.HTMLContent, `<table border="0" cellpadding="0" cellspacing="0" role="presentation"`,
					"tracking pixel should be wrapped in a table")
				assert.NotContains(t, entry.Payload.HTMLContent, "{{ unsubscribe_url }}",
					"Raw Liquid syntax should not appear in HTML")
				assert.NotContains(t, entry.Payload.HTMLContent, "{{ notification_center_url }}",
					"Raw Liquid syntax should not appear in HTML")
				// Verify href is not empty (it was empty before fix)
				assert.NotContains(t, entry.Payload.HTMLContent, `href=""`,
					"Links should have actual URLs, not empty hrefs")

				// RFC-8058 List-Unsubscribe URL should be extracted
				assert.NotEmpty(t, entry.Payload.EmailOptions.ListUnsubscribeURL,
					"oneclick_unsubscribe_url should be set for RFC-8058")
				assert.Contains(t, entry.Payload.EmailOptions.ListUnsubscribeURL, "unsubscribe-oneclick",
					"List-Unsubscribe URL should contain oneclick endpoint")

				return nil
			})

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		sent, failed, err := sender.SendBatch(
			context.Background(),
			"workspace-1",
			"integration-1",
			"test-secret-key",
			"https://api.example.com",
			"",
			true,
			"broadcast-1",
			recipients,
			map[string]*domain.Template{"template-1": template},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"",
		)

		assert.NoError(t, err)
		assert.Equal(t, 1, sent)
		assert.Equal(t, 0, failed)
	})

	t.Run("renders system variables in subject line", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			Name:          "Weekly Newsletter",
			UTMParameters: &domain.UTMParameters{},
		}

		// Template with broadcast.name in subject
		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Newsletter from {{ broadcast.name }}",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		recipients := []*domain.ContactWithList{
			{
				Contact:  &domain.Contact{Email: "user@example.com"},
				ListID:   "list-123",
				ListName: "Subscribers",
			},
		}

		mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
			Return(broadcast, nil)

		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
			DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
				require.Len(t, entries, 1)
				entry := entries[0]

				// Subject should contain rendered broadcast name, not raw Liquid
				assert.Contains(t, entry.Payload.Subject, "Weekly Newsletter",
					"Subject should contain broadcast name")
				assert.NotContains(t, entry.Payload.Subject, "{{ broadcast.name }}",
					"Subject should not contain raw Liquid syntax")
				assert.NotContains(t, entry.Payload.Subject, "{{",
					"Subject should not contain any raw Liquid syntax")

				return nil
			})

		sender := NewQueueMessageSender(
			mockQueueRepo,
			mockBroadcastRepo,
			mockMessageHistoryRepo,
			mockTemplateRepo,
			nil,
			mockLogger,
			nil,
			"https://api.example.com",
		)

		sent, failed, err := sender.SendBatch(
			context.Background(),
			"workspace-1",
			"integration-1",
			"test-secret-key",
			"https://api.example.com",
			"",
			true,
			"broadcast-1",
			recipients,
			map[string]*domain.Template{"template-1": template},
			emailProvider,
			time.Now().Add(5*time.Minute),
			"",
		)

		assert.NoError(t, err)
		assert.Equal(t, 1, sent)
		assert.Equal(t, 0, failed)
	})
}

func TestQueueSendBatch_WithRecipientFeed_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockDataFeedFetcher := bmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()

	emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
	emailProvider := &domain.EmailProvider{
		Kind:    domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{emailSender},
	}

	broadcast := &domain.Broadcast{
		ID:            "broadcast-1",
		WorkspaceID:   "workspace-1",
		Name:          "Feed Broadcast",
		UTMParameters: &domain.UTMParameters{Source: "test", Medium: "email"},
		DataFeed: &domain.DataFeedSettings{
			RecipientFeed: &domain.RecipientFeedSettings{
				Enabled: true,
				URL:     "https://feed.example.com/recipient",
			},
		},
	}

	// Template that uses recipient_feed data
	template := &domain.Template{
		ID: "template-1",
		Email: &domain.EmailTemplate{
			SenderID:         emailSender.ID,
			Subject:          "Your product: {{ recipient_feed.product }}",
			VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Product: {{ recipient_feed.product }}")),
		},
	}

	recipients := []*domain.ContactWithList{
		{
			Contact:  &domain.Contact{Email: "user@example.com"},
			ListID:   "list-1",
			ListName: "Subscribers",
		},
	}

	mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
		Return(broadcast, nil)

	// Mock FetchRecipient to return feed data
	mockDataFeedFetcher.EXPECT().FetchRecipient(
		gomock.Any(),
		broadcast.DataFeed.RecipientFeed,
		gomock.Any(),
	).Times(1).Return(map[string]interface{}{
		"product":  "Widget",
		"_success": true,
	}, nil)

	// Verify enqueued entry contains rendered feed data
	mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
		DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
			require.Len(t, entries, 1)
			entry := entries[0]

			// HTML should contain the rendered recipient_feed.product value
			assert.Contains(t, entry.Payload.HTMLContent, "Widget",
				"HTML should contain rendered recipient_feed.product value")
			assert.NotContains(t, entry.Payload.HTMLContent, "{{ recipient_feed.product }}",
				"HTML should not contain raw Liquid syntax")

			// Subject should also be rendered
			assert.Contains(t, entry.Payload.Subject, "Widget",
				"Subject should contain rendered recipient_feed.product value")

			return nil
		})

	sender := NewQueueMessageSender(
		mockQueueRepo,
		mockBroadcastRepo,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		mockDataFeedFetcher,
		mockLogger,
		nil,
		"https://api.example.com",
	)

	sent, failed, err := sender.SendBatch(
		context.Background(),
		"workspace-1",
		"integration-1",
		"secret-key",
		"https://api.example.com",
		"",
		true,
		"broadcast-1",
		recipients,
		map[string]*domain.Template{"template-1": template},
		emailProvider,
		time.Now().Add(5*time.Minute),
		"",
	)

	assert.NoError(t, err)
	assert.Equal(t, 1, sent)
	assert.Equal(t, 0, failed)
}

func TestQueueSendBatch_WithRecipientFeed_FetchError_PausesBroadcast(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockDataFeedFetcher := bmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()

	emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
	emailProvider := &domain.EmailProvider{
		Kind:    domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{emailSender},
	}

	broadcast := &domain.Broadcast{
		ID:            "broadcast-1",
		WorkspaceID:   "workspace-1",
		Name:          "Feed Broadcast",
		UTMParameters: &domain.UTMParameters{},
		DataFeed: &domain.DataFeedSettings{
			RecipientFeed: &domain.RecipientFeedSettings{
				Enabled: true,
				URL:     "https://feed.example.com/recipient",
			},
		},
	}

	template := &domain.Template{
		ID: "template-1",
		Email: &domain.EmailTemplate{
			SenderID:         emailSender.ID,
			Subject:          "Test Subject",
			VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
		},
	}

	recipients := []*domain.ContactWithList{
		{
			Contact: &domain.Contact{Email: "user@example.com"},
			ListID:  "list-1",
		},
	}

	mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
		Return(broadcast, nil)

	// Mock FetchRecipient to return error (after exhausting retries internally)
	mockDataFeedFetcher.EXPECT().FetchRecipient(
		gomock.Any(),
		broadcast.DataFeed.RecipientFeed,
		gomock.Any(),
	).Times(1).Return(nil, fmt.Errorf("HTTP error 500: Internal Server Error"))

	// Enqueue should NOT be called — entries are discarded on feed failure
	mockQueueRepo.EXPECT().Enqueue(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	sender := NewQueueMessageSender(
		mockQueueRepo,
		mockBroadcastRepo,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		mockDataFeedFetcher,
		mockLogger,
		nil,
		"https://api.example.com",
	)

	sent, failed, err := sender.SendBatch(
		context.Background(),
		"workspace-1",
		"integration-1",
		"secret-key",
		"https://api.example.com",
		"",
		true,
		"broadcast-1",
		recipients,
		map[string]*domain.Template{"template-1": template},
		emailProvider,
		time.Now().Add(5*time.Minute),
		"",
	)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBroadcastShouldPause),
		"Error should wrap ErrBroadcastShouldPause")
	assert.Contains(t, err.Error(), "recipient feed failed")
	assert.Equal(t, 0, sent, "No entries should be reported as sent")
	assert.Equal(t, 0, failed, "No entries should be reported as failed")
}

func TestQueueSendBatch_WithRecipientFeed_Disabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockDataFeedFetcher := bmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

	emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
	emailProvider := &domain.EmailProvider{
		Kind:    domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{emailSender},
	}

	broadcast := &domain.Broadcast{
		ID:            "broadcast-1",
		WorkspaceID:   "workspace-1",
		Name:          "Feed Broadcast",
		UTMParameters: &domain.UTMParameters{Source: "test", Medium: "email"},
		DataFeed: &domain.DataFeedSettings{
			RecipientFeed: &domain.RecipientFeedSettings{
				Enabled: false, // Disabled
				URL:     "https://feed.example.com/recipient",
			},
		},
	}

	template := &domain.Template{
		ID: "template-1",
		Email: &domain.EmailTemplate{
			SenderID:         emailSender.ID,
			Subject:          "Test Subject",
			VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
		},
	}

	recipients := []*domain.ContactWithList{
		{
			Contact:  &domain.Contact{Email: "user@example.com"},
			ListID:   "list-1",
			ListName: "Subscribers",
		},
	}

	mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
		Return(broadcast, nil)

	// FetchRecipient should NOT be called when disabled
	mockDataFeedFetcher.EXPECT().FetchRecipient(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
		DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
			assert.Len(t, entries, 1)
			return nil
		})

	sender := NewQueueMessageSender(
		mockQueueRepo,
		mockBroadcastRepo,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		mockDataFeedFetcher,
		mockLogger,
		nil,
		"https://api.example.com",
	)

	sent, failed, err := sender.SendBatch(
		context.Background(),
		"workspace-1",
		"integration-1",
		"secret-key",
		"https://api.example.com",
		"",
		true,
		"broadcast-1",
		recipients,
		map[string]*domain.Template{"template-1": template},
		emailProvider,
		time.Now().Add(5*time.Minute),
		"",
	)

	assert.NoError(t, err)
	assert.Equal(t, 1, sent)
	assert.Equal(t, 0, failed)
}

func TestQueueSendBatch_WithRecipientFeed_NilFetcher(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

	emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
	emailProvider := &domain.EmailProvider{
		Kind:    domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{emailSender},
	}

	broadcast := &domain.Broadcast{
		ID:            "broadcast-1",
		WorkspaceID:   "workspace-1",
		Name:          "Feed Broadcast",
		UTMParameters: &domain.UTMParameters{Source: "test", Medium: "email"},
		DataFeed: &domain.DataFeedSettings{
			RecipientFeed: &domain.RecipientFeedSettings{
				Enabled: true, // Enabled, but fetcher is nil
				URL:     "https://feed.example.com/recipient",
			},
		},
	}

	template := &domain.Template{
		ID: "template-1",
		Email: &domain.EmailTemplate{
			SenderID:         emailSender.ID,
			Subject:          "Test Subject",
			VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
		},
	}

	recipients := []*domain.ContactWithList{
		{
			Contact:  &domain.Contact{Email: "user@example.com"},
			ListID:   "list-1",
			ListName: "Subscribers",
		},
	}

	mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", "broadcast-1").
		Return(broadcast, nil)

	mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
		DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
			assert.Len(t, entries, 1)
			return nil
		})

	// Create sender with nil dataFeedFetcher
	sender := NewQueueMessageSender(
		mockQueueRepo,
		mockBroadcastRepo,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		nil, // nil dataFeedFetcher
		mockLogger,
		nil,
		"https://api.example.com",
	)

	sent, failed, err := sender.SendBatch(
		context.Background(),
		"workspace-1",
		"integration-1",
		"secret-key",
		"https://api.example.com",
		"",
		true,
		"broadcast-1",
		recipients,
		map[string]*domain.Template{"template-1": template},
		emailProvider,
		time.Now().Add(5*time.Minute),
		"",
	)

	assert.NoError(t, err)
	assert.Equal(t, 1, sent, "Should still enqueue normally with nil fetcher")
	assert.Equal(t, 0, failed)
}

func TestQueueMessageSender_SelectTemplate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	sender := NewQueueMessageSender(
		mockQueueRepo,
		mockBroadcastRepo,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		nil,
		mockLogger,
		nil,
		"https://api.example.com",
	)

	qms := sender.(*queueMessageSender)
	broadcast := &domain.Broadcast{ID: "broadcast-1"}

	t.Run("returns nil for empty templates", func(t *testing.T) {
		result := qms.selectTemplate(map[string]*domain.Template{}, broadcast)
		assert.Nil(t, result)
	})

	t.Run("returns single template when only one", func(t *testing.T) {
		template := &domain.Template{ID: "template-1"}
		templates := map[string]*domain.Template{
			"template-1": template,
		}

		result := qms.selectTemplate(templates, broadcast)
		assert.NotNil(t, result)
		assert.Equal(t, "template-1", result.ID)
	})

	t.Run("randomly selects for A/B testing", func(t *testing.T) {
		template1 := &domain.Template{ID: "template-1"}
		template2 := &domain.Template{ID: "template-2"}
		templates := map[string]*domain.Template{
			"template-1": template1,
			"template-2": template2,
		}

		// Run multiple times to verify randomness
		selections := make(map[string]int)
		for i := 0; i < 20; i++ {
			result := qms.selectTemplate(templates, broadcast)
			require.NotNil(t, result)
			selections[result.ID]++
		}

		// Both templates should be selected at least once
		assert.Greater(t, selections["template-1"]+selections["template-2"], 0)
	})
}

func TestQueueMessageSender_BuildQueueEntry(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	sender := NewQueueMessageSender(
		mockQueueRepo,
		mockBroadcastRepo,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		nil,
		mockLogger,
		nil,
		"https://api.example.com",
	)

	qms := sender.(*queueMessageSender)

	t.Run("builds entry with all required fields", func(t *testing.T) {
		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:               domain.EmailProviderKindSMTP,
			Senders:            []domain.EmailSender{emailSender},
			RateLimitPerMinute: 100,
		}

		broadcast := &domain.Broadcast{
			ID:          "broadcast-1",
			WorkspaceID: "workspace-1",
			UTMParameters: &domain.UTMParameters{
				Source:   "newsletter",
				Medium:   "email",
				Campaign: "weekly",
			},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Hello {{ contact.name }}",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		data := map[string]interface{}{
			"contact": map[string]interface{}{
				"name":  "John",
				"email": "john@example.com",
			},
		}

		entry, err := qms.buildQueueEntry(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-123",
			"john@example.com",
			template,
			data,
			emailProvider,
			"",
			"",
			nil, // Veridian: contact (pixel par classe)
			newVeridianWorkspacePixelResolver(nil, qms.logger), // Veridian: pixel resolver (nil repo = pas de fallback workspace)
		)

		require.NoError(t, err)
		require.NotNil(t, entry)

		assert.NotEmpty(t, entry.ID)
		assert.Equal(t, domain.EmailQueueStatusPending, entry.Status)
		assert.Equal(t, domain.EmailQueuePriorityMarketing, entry.Priority)
		assert.Equal(t, domain.EmailQueueSourceBroadcast, entry.SourceType)
		assert.Equal(t, "broadcast-1", entry.SourceID)
		assert.Equal(t, "integration-1", entry.IntegrationID)
		assert.Equal(t, domain.EmailProviderKindSMTP, entry.ProviderKind)
		assert.Equal(t, "john@example.com", entry.ContactEmail)
		assert.Equal(t, "msg-123", entry.MessageID)
		assert.Equal(t, "template-1", entry.TemplateID)
		assert.Equal(t, "sender@example.com", entry.Payload.FromAddress)
		assert.Equal(t, "Test Sender", entry.Payload.FromName)
		assert.Contains(t, entry.Payload.Subject, "Hello")
		assert.NotEmpty(t, entry.Payload.HTMLContent)
		assert.Equal(t, 100, entry.Payload.RateLimitPerMinute)
		assert.Equal(t, 3, entry.MaxAttempts)
		// Anti-hash : sans dedup injecté → aucun hash posé (non-régression upstream).
		assert.Empty(t, entry.Payload.VeridianContentHash)
	})

	// Veridian fork — anti-hash : avec le dedup injecté ET un contexte cold
	// (broadcast porteur de rates par classe), buildQueueEntry pose le hash du
	// rendu final sur le payload (alimente la fenêtre glissante). Couvre le
	// câblage SetVeridianContentDedup + l'appel Resolve dans buildQueueEntry.
	t.Run("anti-hash pose le content hash en contexte cold", func(t *testing.T) {
		mockMessageHistoryRepo.EXPECT().
			ExistsContentHashSince(gomock.Any(), "workspace-1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(false, nil) // pas de collision → hash initial retenu
		qms.SetVeridianContentDedup(newVeridianContentDedup(mockMessageHistoryRepo, qms.logger))
		defer qms.SetVeridianContentDedup(nil) // ne pas fuiter sur les autres sous-tests

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:               domain.EmailProviderKindSMTP,
			Senders:            []domain.EmailSender{emailSender},
			RateLimitPerMinute: 100,
		}
		// Contexte cold : rates par classe sur le broadcast → VeridianIsColdContext=true.
		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			WorkspaceID:   "workspace-1",
			UTMParameters: &domain.UTMParameters{},
			Metadata: domain.MapOfAny{
				domain.VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0},
			},
		}
		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Bonjour",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		entry, err := qms.buildQueueEntry(
			context.Background(), "workspace-1", "integration-1", "https://api.test.com", true,
			broadcast, "msg-123", "john@gmail.com", template, map[string]interface{}{},
			emailProvider, "", "", nil,
			newVeridianWorkspacePixelResolver(nil, qms.logger),
		)
		require.NoError(t, err)
		require.NotNil(t, entry)
		// Le hash doit être posé (32 hex = SHA-256 tronqué 128 bits).
		assert.Len(t, entry.Payload.VeridianContentHash, 32)
		assert.Equal(t, domain.VeridianContentHash(entry.Payload.Subject, entry.Payload.HTMLContent), entry.Payload.VeridianContentHash)
	})

	t.Run("extracts List-Unsubscribe URL from data", func(t *testing.T) {
		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			UTMParameters: &domain.UTMParameters{},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		data := map[string]interface{}{
			"oneclick_unsubscribe_url": "https://example.com/unsubscribe?token=abc123",
		}

		entry, err := qms.buildQueueEntry(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-123",
			"test@example.com",
			template,
			data,
			emailProvider,
			"",
			"",
			nil, // Veridian: contact (pixel par classe)
			newVeridianWorkspacePixelResolver(nil, qms.logger), // Veridian: pixel resolver (nil repo = pas de fallback workspace)
		)

		require.NoError(t, err)
		assert.Equal(t, "https://example.com/unsubscribe?token=abc123", entry.Payload.EmailOptions.ListUnsubscribeURL)
	})

	// Veridian fork (cold outbound) — en contexte tunnel cold, l'unsubscribe ne
	// doit PAS être propagé MÊME si data porte oneclick_unsubscribe_url (sinon
	// header RFC-8058 List-Unsubscribe = signal mailing de masse qui tue la
	// délivrabilité). Le contexte cold est ici signalé par config cold sur le
	// broadcast (rates) ET, dans un second cas, par le tag contact custom_string_5.
	t.Run("Veridian: cold context suppresses List-Unsubscribe URL", func(t *testing.T) {
		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		data := map[string]interface{}{
			"oneclick_unsubscribe_url": "https://example.com/unsubscribe?token=abc123",
		}

		// Cas 1 : contexte cold signalé par config rates sur le broadcast.
		coldBroadcast := &domain.Broadcast{
			ID:            "broadcast-cold",
			UTMParameters: &domain.UTMParameters{},
			Metadata: domain.MapOfAny{
				domain.VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0},
			},
		}
		entry, err := qms.buildQueueEntry(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			coldBroadcast,
			"msg-cold-1",
			"lead@gmail.com",
			template,
			data,
			emailProvider,
			"",
			"",
			nil, // contact non chargé sur ce chemin
			newVeridianWorkspacePixelResolver(nil, qms.logger),
		)
		require.NoError(t, err)
		assert.Empty(t, entry.Payload.EmailOptions.ListUnsubscribeURL,
			"cold context (broadcast rates) doit supprimer List-Unsubscribe malgré oneclick_unsubscribe_url")

		// Cas 2 : contexte cold signalé par le tag contact custom_string_5, sur un
		// broadcast SANS config cold (le tag suffit à activer le tunnel).
		plainBroadcast := &domain.Broadcast{
			ID:            "broadcast-plain",
			UTMParameters: &domain.UTMParameters{},
		}
		coldContact := &domain.Contact{
			Email:         "lead@gmail.com",
			CustomString5: &domain.NullableString{String: domain.ProviderClassGoogle},
		}
		entry2, err := qms.buildQueueEntry(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			plainBroadcast,
			"msg-cold-2",
			"lead@gmail.com",
			template,
			data,
			emailProvider,
			"",
			"",
			coldContact, // tag cold → tunnel actif
			newVeridianWorkspacePixelResolver(nil, qms.logger),
		)
		require.NoError(t, err)
		assert.Empty(t, entry2.Payload.EmailOptions.ListUnsubscribeURL,
			"cold context (tag contact) doit supprimer List-Unsubscribe malgré oneclick_unsubscribe_url")
	})

	t.Run("returns error when no sender configured", func(t *testing.T) {
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{}, // No senders
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			UTMParameters: &domain.UTMParameters{},
		}

		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID: "non-existent",
				Subject:  "Test",
			},
		}

		_, err := qms.buildQueueEntry(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-123",
			"test@example.com",
			template,
			nil,
			emailProvider,
			"",
			"",
			nil, // Veridian: contact (pixel par classe)
			newVeridianWorkspacePixelResolver(nil, qms.logger), // Veridian: pixel resolver (nil repo = pas de fallback workspace)
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no sender configured")
	})

	t.Run("preserves ReplyTo from template", func(t *testing.T) {
		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
		}

		broadcast := &domain.Broadcast{
			ID:            "broadcast-1",
			UTMParameters: &domain.UTMParameters{},
		}

		// Template with ReplyTo set
		template := &domain.Template{
			ID: "template-1",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "Test Subject",
				ReplyTo:          "support@example.com", // ReplyTo is set
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		entry, err := qms.buildQueueEntry(
			context.Background(),
			"workspace-1",
			"integration-1",
			"https://api.test.com",
			true,
			broadcast,
			"msg-123",
			"test@example.com",
			template,
			map[string]interface{}{},
			emailProvider,
			"",
			"",
			nil, // Veridian: contact (pixel par classe)
			newVeridianWorkspacePixelResolver(nil, qms.logger), // Veridian: pixel resolver (nil repo = pas de fallback workspace)
		)

		require.NoError(t, err)
		// This assertion should FAIL before the fix
		assert.Equal(t, "support@example.com", entry.Payload.EmailOptions.ReplyTo,
			"ReplyTo from template should be preserved in queue entry")
	})
}

// Veridian fork — round-robin multi-SMTP à l'enqueue (queue_message_sender.go).
// Couvre SetVeridianSenderRotator + la sélection cold dans buildQueueEntry :
// en contexte cold avec 3 senders et un template SANS SenderID fixe, le
// FromAddress du payload tourne d'un recipient à l'autre ; hors cold, le sender
// par défaut reste figé (non-régression upstream).
func TestQueueMessageSender_VeridianColdRoundRobin(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	newSender := func() *queueMessageSender {
		s := NewQueueMessageSender(mockQueueRepo, mockBroadcastRepo, mockMessageHistoryRepo,
			mockTemplateRepo, nil, mockLogger, nil, "https://api.example.com").(*queueMessageSender)
		s.SetVeridianSenderRotator(domain.NewVeridianSenderRotator())
		return s
	}

	t.Run("cold round-robin rotates sender per recipient", func(t *testing.T) {
		emailProvider := &domain.EmailProvider{
			Kind:               domain.EmailProviderKindSMTP,
			RateLimitPerMinute: 10,
			Senders: []domain.EmailSender{
				{ID: "s1", Email: "a@agences-veridian.fr", Name: "A", IsDefault: true},
				{ID: "s2", Email: "b@agences-veridian.fr", Name: "B"},
				{ID: "s3", Email: "c@agences-veridian.fr", Name: "C"},
			},
		}
		// Broadcast cold (rates dans metadata) → contexte cold détecté.
		broadcast := &domain.Broadcast{
			ID:            "broadcast-rr",
			UTMParameters: &domain.UTMParameters{},
			Metadata:      domain.MapOfAny{domain.VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0}},
		}
		// Template SANS SenderID fixe → la rotation choisit.
		template := &domain.Template{
			ID: "template-rr",
			Email: &domain.EmailTemplate{
				SenderID:         "",
				Subject:          "Hi",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
			},
		}

		s := newSender()
		resolver := newVeridianWorkspacePixelResolver(nil, s.logger)

		var froms []string
		for _, email := range []string{"u1@gmail.com", "u2@gmail.com", "u3@gmail.com", "u4@gmail.com"} {
			entry, err := s.buildQueueEntry(context.Background(), "workspace-1", "integration-1",
				"https://api.test.com", true, broadcast, "msg-"+email, email, template,
				map[string]interface{}{}, emailProvider, "", "", nil, resolver)
			require.NoError(t, err)
			froms = append(froms, entry.Payload.FromAddress)
		}

		// Round-robin trié par ID : s1, s2, s3, s1.
		assert.Equal(t, []string{
			"a@agences-veridian.fr", "b@agences-veridian.fr",
			"c@agences-veridian.fr", "a@agences-veridian.fr",
		}, froms)
	})

	t.Run("non-cold keeps default sender (no rotation)", func(t *testing.T) {
		emailProvider := &domain.EmailProvider{
			Kind:               domain.EmailProviderKindSMTP,
			RateLimitPerMinute: 10,
			Senders: []domain.EmailSender{
				{ID: "s1", Email: "a@a.fr", Name: "A", IsDefault: true},
				{ID: "s2", Email: "b@a.fr", Name: "B"},
			},
		}
		broadcast := &domain.Broadcast{ID: "b-plain", UTMParameters: &domain.UTMParameters{}, Metadata: domain.MapOfAny{}}
		template := &domain.Template{
			ID:    "t-plain",
			Email: &domain.EmailTemplate{SenderID: "", Subject: "Hi", VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello"))},
		}

		s := newSender()
		resolver := newVeridianWorkspacePixelResolver(nil, s.logger)

		for _, email := range []string{"u1@gmail.com", "u2@gmail.com", "u3@gmail.com"} {
			entry, err := s.buildQueueEntry(context.Background(), "workspace-1", "integration-1",
				"https://api.test.com", true, broadcast, "msg-"+email, email, template,
				map[string]interface{}{}, emailProvider, "", "", nil, resolver)
			require.NoError(t, err)
			assert.Equal(t, "a@a.fr", entry.Payload.FromAddress, "hors cold : sender par défaut figé")
		}
	})
}

// Veridian — l'enqueue doit propager la config de throttle par classe de
// provider destinataire dans le payload : débits du broadcast (metadata) +
// tag contact custom_string_5 posé par l'export batch (contrat provider_class).
// Sans config ni tag, le payload reste strictement upstream.
// Veridian fork — bout-en-bout du FIX fallback workspace pixel (2026-06-13) :
// SetVeridianWorkspaceRepo injecté + workspace avec config pixel par classe →
// le HTML RÉELLEMENT compilé puis enqueué reflète la politique pixel posée AU
// NIVEAU WORKSPACE (chemin UI Settings → Cold outreach). Avant le fix, les
// senders passaient workspace=nil et cette config était silencieusement ignorée.
//
// Le pixel d'ouverture apparaît comme un chemin /t/ chiffré dans le HTML
// (cf. GenerateHTMLOpenTrackingPixel). On vérifie ON/OFF par classe + le fait
// que la réécriture de liens (/r/) reste appliquée indépendamment.
func TestQueueMessageSender_SendBatch_VeridianWorkspacePixelFallback(t *testing.T) {
	emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
	emailProvider := &domain.EmailProvider{
		Kind:    domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{emailSender},
	}
	template := &domain.Template{
		ID: "template-1",
		Email: &domain.EmailTemplate{
			SenderID: emailSender.ID,
			Subject:  "Hello",
			// Le tree contient un lien → on peut vérifier /r/ (clics) séparément.
			VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1",
				`<a href="https://example.com">Voir</a>`)),
		},
	}
	templates := map[string]*domain.Template{"template-1": template}

	// Broadcast SANS config pixel → la politique vient UNIQUEMENT du workspace.
	broadcast := &domain.Broadcast{
		ID:            "broadcast-wpx",
		WorkspaceID:   "workspace-1",
		UTMParameters: &domain.UTMParameters{},
	}

	// Workspace : pixel OFF sur google, ON sur freemail_fr. C'est la config que
	// l'UI persiste et que le fix doit faire respecter à l'envoi.
	workspace := &domain.Workspace{
		ID: "workspace-1",
		Settings: domain.WorkspaceSettings{
			VeridianOpenPixelByClass: map[string]bool{
				"google":      false,
				"freemail_fr": true,
			},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

	mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", broadcast.ID).Return(broadcast, nil)
	// Le fallback workspace ne doit charger le workspace qu'UNE fois pour tout
	// le batch (mémoïsation), pas une fois par recipient.
	mockWorkspaceRepo.EXPECT().GetByID(gomock.Any(), "workspace-1").Return(workspace, nil).Times(1)

	var enqueued []*domain.EmailQueueEntry
	mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, entries []*domain.EmailQueueEntry) error {
			enqueued = entries
			return nil
		})

	sender := NewQueueMessageSender(
		mockQueueRepo, mockBroadcastRepo, mockMessageHistoryRepo,
		mockTemplateRepo, nil, mockLogger, nil, "https://api.example.com",
	)
	// LE FIX : sans cet appel, workspace=nil et le test échouerait (pixel google
	// présent car EnableTracking=true → comportement upstream).
	sender.(*queueMessageSender).SetVeridianWorkspaceRepo(mockWorkspaceRepo)

	recipients := []*domain.ContactWithList{
		{Contact: &domain.Contact{Email: "lead@gmail.com"}, ListID: "list-1"}, // google → pixel OFF
		{Contact: &domain.Contact{Email: "lead@orange.fr"}, ListID: "list-1"}, // freemail_fr → pixel ON
	}

	sent, failed, err := sender.SendBatch(
		context.Background(), "workspace-1", "integration-1", "secret-key",
		"https://api.example.com", "", true, broadcast.ID, recipients,
		templates, emailProvider, time.Now().Add(5*time.Minute), "",
	)
	require.NoError(t, err)
	require.Equal(t, 2, sent)
	require.Equal(t, 0, failed)

	require.Len(t, enqueued, 2)
	byEmail := map[string]*domain.EmailQueueEntry{}
	for _, e := range enqueued {
		byEmail[e.ContactEmail] = e
	}

	gmail := byEmail["lead@gmail.com"]
	require.NotNil(t, gmail)
	assert.NotContains(t, gmail.Payload.HTMLContent, "/t/",
		"workspace pixel google=false doit supprimer le pixel d'ouverture à l'envoi")
	assert.Contains(t, gmail.Payload.HTMLContent, "/r/",
		"les clics restent trackés (EnableTracking=true) malgré le pixel OFF")

	orange := byEmail["lead@orange.fr"]
	require.NotNil(t, orange)
	assert.Contains(t, orange.Payload.HTMLContent, "/t/",
		"workspace pixel freemail_fr=true doit conserver le pixel d'ouverture")
}

func TestQueueMessageSender_SendBatch_VeridianProviderThrottle(t *testing.T) {
	newSender := func(ctrl *gomock.Controller, broadcast *domain.Broadcast, onEnqueue func([]*domain.EmailQueueEntry)) MessageSender {
		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)

		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

		mockBroadcastRepo.EXPECT().GetBroadcast(gomock.Any(), "workspace-1", broadcast.ID).
			Return(broadcast, nil)
		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
			DoAndReturn(func(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error {
				onEnqueue(entries)
				return nil
			})

		return NewQueueMessageSender(
			mockQueueRepo, mockBroadcastRepo, mockMessageHistoryRepo,
			mockTemplateRepo, nil, mockLogger, nil, "https://api.example.com",
		)
	}

	emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
	emailProvider := &domain.EmailProvider{
		Kind:    domain.EmailProviderKindSMTP,
		Senders: []domain.EmailSender{emailSender},
	}
	template := &domain.Template{
		ID: "template-1",
		Email: &domain.EmailTemplate{
			SenderID:         emailSender.ID,
			Subject:          "Test Subject",
			VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "Hello")),
		},
	}
	templates := map[string]*domain.Template{"template-1": template}

	sendBatch := func(s MessageSender, broadcastID string, recipients []*domain.ContactWithList) {
		sent, failed, err := s.SendBatch(
			context.Background(), "workspace-1", "integration-1", "secret-key",
			"https://api.example.com", "", true, broadcastID, recipients,
			templates, emailProvider, time.Now().Add(5*time.Minute), "",
		)
		require.NoError(t, err)
		require.Equal(t, len(recipients), sent)
		require.Equal(t, 0, failed)
	}

	t.Run("rates from broadcast metadata + contact tag propagated", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		broadcast := &domain.Broadcast{
			ID:          "broadcast-vt",
			WorkspaceID: "workspace-1",
			Metadata: domain.MapOfAny{
				domain.VeridianProviderClassRatesMetadataKey: map[string]any{
					"google": 0.5, "corporate": 30.0,
				},
			},
			UTMParameters: &domain.UTMParameters{},
		}

		recipients := []*domain.ContactWithList{
			{
				// taggué google par l'export (ex. MX Google Workspace résolu en amont)
				Contact: &domain.Contact{
					Email:         "tagged@boitepro.fr",
					CustomString5: &domain.NullableString{String: "google"},
				},
				ListID: "list-1",
			},
			{
				// non taggué : le worker classifiera par suffixe
				Contact: &domain.Contact{Email: "untagged@acme-corp.com"},
				ListID:  "list-1",
			},
		}

		sender := newSender(ctrl, broadcast, func(entries []*domain.EmailQueueEntry) {
			require.Len(t, entries, 2)
			byEmail := map[string]*domain.EmailQueueEntry{}
			for _, e := range entries {
				byEmail[e.ContactEmail] = e
			}

			tagged := byEmail["tagged@boitepro.fr"]
			require.NotNil(t, tagged)
			assert.Equal(t, "google", tagged.Payload.VeridianProviderClass)
			assert.Equal(t, map[string]float64{"google": 0.5, "corporate": 30},
				tagged.Payload.VeridianProviderClassRates)

			untagged := byEmail["untagged@acme-corp.com"]
			require.NotNil(t, untagged)
			assert.Empty(t, untagged.Payload.VeridianProviderClass)
			assert.Equal(t, map[string]float64{"google": 0.5, "corporate": 30},
				untagged.Payload.VeridianProviderClassRates)
		})

		sendBatch(sender, "broadcast-vt", recipients)
	})

	t.Run("no metadata and no tag leaves payload strictly upstream", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		broadcast := &domain.Broadcast{
			ID:            "broadcast-plain",
			WorkspaceID:   "workspace-1",
			UTMParameters: &domain.UTMParameters{},
		}

		recipients := []*domain.ContactWithList{
			{Contact: &domain.Contact{Email: "user@example.com"}, ListID: "list-1"},
		}

		sender := newSender(ctrl, broadcast, func(entries []*domain.EmailQueueEntry) {
			require.Len(t, entries, 1)
			assert.Empty(t, entries[0].Payload.VeridianProviderClass)
			assert.Nil(t, entries[0].Payload.VeridianProviderClassRates)
		})

		sendBatch(sender, "broadcast-plain", recipients)
	})
}

// TestQueueMessageSender_SpintaxResolvedPerRecipient vérifie le câblage Veridian
// du spintax (Lot 6) dans le queue sender : le sujet ET le corps HTML enqueués
// sont résolus par destinataire, avec une graine déterministe = l'email du
// contact. C'est un test de comportement réel (entry capturée), pas un mock du
// resolver : on s'assure que la variation cold est effectivement appliquée à
// l'envoi et qu'elle est stable par destinataire (re-render = même variante).
func TestQueueMessageSender_SpintaxResolvedPerRecipient(t *testing.T) {
	newSenderWithCapture := func(t *testing.T) (MessageSender, *domain.EmailProvider, *domain.Broadcast, *domain.Template, *[]*domain.EmailQueueEntry) {
		t.Helper()
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
		mockBroadcastRepo := mocks.NewMockBroadcastRepository(ctrl)
		mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
		mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
		mockLogger := pkgmocks.NewMockLogger(ctrl)
		mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
		mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()

		emailSender := domain.NewEmailSender("sender@example.com", "Test Sender")
		emailProvider := &domain.EmailProvider{
			Kind:    domain.EmailProviderKindSMTP,
			Senders: []domain.EmailSender{emailSender},
			SMTP:    &domain.SMTPSettings{Host: "smtp.example.com", Port: 587},
		}
		broadcast := &domain.Broadcast{
			ID:            "broadcast-spintax",
			WorkspaceID:   "workspace-1",
			Name:          "Spintax Broadcast",
			UTMParameters: &domain.UTMParameters{},
		}
		// Sujet ET corps porteurs de spintax.
		template := &domain.Template{
			ID: "template-spintax",
			Email: &domain.EmailTemplate{
				SenderID:         emailSender.ID,
				Subject:          "{Offre|Promo} exclusive",
				VisualEditorTree: createQueueValidTestTree(createQueueTestTextBlock("txt1", "{Bonjour|Salut} cher client")),
			},
		}

		var captured []*domain.EmailQueueEntry
		mockQueueRepo.EXPECT().Enqueue(gomock.Any(), "workspace-1", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, entries []*domain.EmailQueueEntry) error {
				captured = append(captured, entries...)
				return nil
			}).AnyTimes()

		sender := NewQueueMessageSender(
			mockQueueRepo, mockBroadcastRepo, mockMessageHistoryRepo, mockTemplateRepo,
			nil, mockLogger, nil, "https://api.example.com",
		)
		return sender, emailProvider, broadcast, template, &captured
	}

	send := func(t *testing.T, sender MessageSender, ep *domain.EmailProvider, b *domain.Broadcast, tpl *domain.Template, email string) {
		t.Helper()
		err := sender.SendToRecipient(
			context.Background(), "workspace-1", "integration-1", "https://api.test.com",
			false, b, "msg-"+email, email, tpl,
			map[string]interface{}{"contact": map[string]interface{}{"email": email}},
			ep, time.Now().Add(5*time.Minute), "", "",
		)
		require.NoError(t, err)
	}

	t.Run("subject and body are resolved (no raw spintax leaks)", func(t *testing.T) {
		sender, ep, b, tpl, captured := newSenderWithCapture(t)
		send(t, sender, ep, b, tpl, "alice@example.com")

		require.Len(t, *captured, 1)
		entry := (*captured)[0]

		// Sujet : une variante exacte, jamais le spintax brut.
		assert.Contains(t, []string{"Offre exclusive", "Promo exclusive"}, entry.Payload.Subject,
			"sujet doit être une variante résolue, eu %q", entry.Payload.Subject)
		assert.NotContains(t, entry.Payload.Subject, "|")

		// Corps : variante résolue présente, spintax brut absent.
		body := entry.Payload.HTMLContent
		bonjour := strings.Contains(body, "Bonjour cher client")
		salut := strings.Contains(body, "Salut cher client")
		assert.True(t, bonjour != salut, "exactement une variante de corps attendue (bonjour=%v salut=%v)", bonjour, salut)
		assert.NotContains(t, body, "{Bonjour|Salut}", "spintax brut du corps ne doit pas subsister")
	})

	t.Run("same recipient yields the same variant (deterministic)", func(t *testing.T) {
		sender, ep, b, tpl, captured := newSenderWithCapture(t)
		send(t, sender, ep, b, tpl, "bob@example.com")
		send(t, sender, ep, b, tpl, "bob@example.com")

		require.Len(t, *captured, 2)
		assert.Equal(t, (*captured)[0].Payload.Subject, (*captured)[1].Payload.Subject,
			"même destinataire = même variante de sujet")
		assert.Equal(t, (*captured)[0].Payload.HTMLContent, (*captured)[1].Payload.HTMLContent,
			"même destinataire = même variante de corps")
	})

	t.Run("different recipients can yield different variants", func(t *testing.T) {
		sender, ep, b, tpl, captured := newSenderWithCapture(t)
		// Sujet à 2 options : sur un échantillon de destinataires, les deux
		// variantes doivent apparaître (sinon la variation cold est inopérante).
		emails := []string{
			"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com",
			"f@x.com", "g@x.com", "h@x.com", "i@x.com", "j@x.com",
		}
		for _, e := range emails {
			send(t, sender, ep, b, tpl, e)
		}
		seen := map[string]bool{}
		for _, entry := range *captured {
			seen[entry.Payload.Subject] = true
		}
		assert.Greater(t, len(seen), 1, "des destinataires différents doivent recevoir des sujets différents")
	})
}
