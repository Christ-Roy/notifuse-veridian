package broadcast

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain/mocks"
	broadcastmocks "github.com/Notifuse/notifuse/internal/service/broadcast/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestNewFactory(t *testing.T) {
	// Create mock controller
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock dependencies
	mockBroadcastRepository := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockTaskRepo := mocks.NewMockTaskRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockDataFeedFetcher := broadcastmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockEventBus := mocks.NewMockEventBus(ctrl)
	config := DefaultConfig()

	tests := []struct {
		name   string
		config *Config
	}{
		{
			name:   "With config provided",
			config: config,
		},
		{
			name:   "With nil config",
			config: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := NewFactory(
				mockBroadcastRepository,
				mockMessageHistoryRepo,
				mockTemplateRepo,
				mockEmailService,
				mockContactRepo,
				mockTaskRepo,
				mockWorkspaceRepo,
				mockEmailQueueRepo,
				mockDataFeedFetcher,
				mockLogger,
				tt.config,
				"https://api.notifuse.com",
				mockEventBus,
				false, // useQueueSender
			)

			assert.NotNil(t, factory)
			assert.Equal(t, mockBroadcastRepository, factory.broadcastRepo)
			assert.Equal(t, mockTemplateRepo, factory.templateRepo)
			assert.Equal(t, mockEmailService, factory.emailService)
			assert.Equal(t, mockContactRepo, factory.contactRepo)
			assert.Equal(t, mockTaskRepo, factory.taskRepo)
			assert.Equal(t, mockLogger, factory.logger)

			// Check if config was set to default when nil was provided
			if tt.config == nil {
				assert.NotNil(t, factory.config)
			} else {
				assert.Equal(t, tt.config, factory.config)
			}
		})
	}
}

func TestFactory_CreateMessageSender(t *testing.T) {
	// Create mock controller
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock dependencies
	mockBroadcastRepository := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockTaskRepo := mocks.NewMockTaskRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockDataFeedFetcher := broadcastmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockEventBus := mocks.NewMockEventBus(ctrl)
	config := DefaultConfig()

	factory := NewFactory(
		mockBroadcastRepository,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		mockEmailService,
		mockContactRepo,
		mockTaskRepo,
		mockWorkspaceRepo,
		mockEmailQueueRepo,
		mockDataFeedFetcher,
		mockLogger,
		config,
		"https://api.notifuse.com",
		mockEventBus,
		false, // useQueueSender
	)

	// Test CreateMessageSender
	messageSender := factory.CreateMessageSender()

	// Assert messageSender is not nil and is of MessageSender type
	assert.NotNil(t, messageSender)
	// messageSender already has type MessageSender, no need for type assertion
}

// Veridian fork — vérifie que CreateMessageSender injecte le workspaceRepo dans
// le sender (les deux chemins : queue + direct). Sans cette injection, le
// fallback workspace du pixel par classe est mort (bug corrigé 2026-06-13).
func TestFactory_CreateMessageSender_InjectsVeridianWorkspaceRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)

	newFactory := func(useQueueSender bool) *Factory {
		return NewFactory(
			mocks.NewMockBroadcastRepository(ctrl),
			mocks.NewMockMessageHistoryRepository(ctrl),
			mocks.NewMockTemplateRepository(ctrl),
			mocks.NewMockEmailServiceInterface(ctrl),
			mocks.NewMockContactRepository(ctrl),
			mocks.NewMockTaskRepository(ctrl),
			mockWorkspaceRepo,
			mocks.NewMockEmailQueueRepository(ctrl),
			broadcastmocks.NewMockDataFeedFetcher(ctrl),
			pkgmocks.NewMockLogger(ctrl),
			DefaultConfig(),
			"https://api.notifuse.com",
			mocks.NewMockEventBus(ctrl),
			useQueueSender,
		)
	}

	t.Run("queue sender path", func(t *testing.T) {
		sender := newFactory(true).CreateMessageSender()
		qms, ok := sender.(*queueMessageSender)
		assert.True(t, ok, "useQueueSender=true doit produire un queueMessageSender")
		assert.Same(t, mockWorkspaceRepo, qms.veridianWorkspaceRepo,
			"le workspaceRepo de la factory doit être injecté dans le queue sender")
	})

	t.Run("direct sender path", func(t *testing.T) {
		sender := newFactory(false).CreateMessageSender()
		ms, ok := sender.(*messageSender)
		assert.True(t, ok, "useQueueSender=false doit produire un messageSender direct")
		assert.Same(t, mockWorkspaceRepo, ms.veridianWorkspaceRepo,
			"le workspaceRepo de la factory doit être injecté dans le sender direct")
	})
}

func TestFactory_CreateOrchestrator(t *testing.T) {
	// Create mock controller
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock dependencies
	mockBroadcastRepository := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockTaskRepo := mocks.NewMockTaskRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockDataFeedFetcher := broadcastmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockEventBus := mocks.NewMockEventBus(ctrl)
	config := DefaultConfig()

	factory := NewFactory(
		mockBroadcastRepository,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		mockEmailService,
		mockContactRepo,
		mockTaskRepo,
		mockWorkspaceRepo,
		mockEmailQueueRepo,
		mockDataFeedFetcher,
		mockLogger,
		config,
		"https://api.notifuse.com",
		mockEventBus,
		false, // useQueueSender
	)

	// Test CreateOrchestrator
	orchestrator := factory.CreateOrchestrator()

	// Assert orchestrator is not nil
	assert.NotNil(t, orchestrator)
	// orchestrator already has type BroadcastOrchestratorInterface, no need for type assertion
}

func TestFactory_RegisterWithTaskService(t *testing.T) {
	// Create mock controller
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock dependencies
	mockBroadcastRepository := mocks.NewMockBroadcastRepository(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockTemplateRepo := mocks.NewMockTemplateRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockTaskRepo := mocks.NewMockTaskRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockDataFeedFetcher := broadcastmocks.NewMockDataFeedFetcher(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockTaskService := mocks.NewMockTaskService(ctrl)
	mockEventBus := mocks.NewMockEventBus(ctrl)
	config := DefaultConfig()

	// Setup expectations
	mockTaskService.EXPECT().RegisterProcessor(gomock.Any()).Return()
	mockLogger.EXPECT().Info(gomock.Any()).Return()

	factory := NewFactory(
		mockBroadcastRepository,
		mockMessageHistoryRepo,
		mockTemplateRepo,
		mockEmailService,
		mockContactRepo,
		mockTaskRepo,
		mockWorkspaceRepo,
		mockEmailQueueRepo,
		mockDataFeedFetcher,
		mockLogger,
		config,
		"https://api.notifuse.com",
		mockEventBus,
		false, // useQueueSender
	)

	// Test RegisterWithTaskService
	factory.RegisterWithTaskService(mockTaskService)

	// With gomock, assertions are verified automatically when ctrl.Finish() is called
}
