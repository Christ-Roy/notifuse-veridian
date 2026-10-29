// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/list_service_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// newSubscribeResultsService wires a ListService whose logger accepts anything:
// these tests are about what comes back, not about what gets logged on the way.
func newSubscribeResultsService(ctrl *gomock.Controller) (*ListService, *mocks.MockListRepository, *mocks.MockWorkspaceRepository, *mocks.MockContactListRepository, *mocks.MockContactRepository, *mocks.MockAuthService) {
	mockRepo := mocks.NewMockListRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockContactListRepo := mocks.NewMockContactListRepository(ctrl)
	mockContactRepo := mocks.NewMockContactRepository(ctrl)
	mockAuthService := mocks.NewMockAuthService(ctrl)
	mockMessageHistoryRepo := mocks.NewMockMessageHistoryRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockCache := pkgmocks.NewMockCache(ctrl)

	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	service := NewListService(mockRepo, mockWorkspaceRepo, mockContactListRepo, mockContactRepo,
		mockMessageHistoryRepo, mockAuthService, mockEmailService, mockLogger, "https://api.example.com", mockCache)

	return service, mockRepo, mockWorkspaceRepo, mockContactListRepo, mockContactRepo, mockAuthService
}

func subscribeResultsWorkspace(workspaceID string) *domain.Workspace {
	// No marketing provider: the welcome/double-opt-in email path is a separate
	// concern and is covered by its own tests.
	return &domain.Workspace{
		ID:       workspaceID,
		Settings: domain.WorkspaceSettings{SecretKey: "test-secret-key"},
	}
}

func subscribeResultsUserWorkspace(workspaceID string) *domain.UserWorkspace {
	return &domain.UserWorkspace{
		UserID:      "user123",
		WorkspaceID: workspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceLists: {Read: true, Write: true},
		},
	}
}

// TestListService_SubscribeToLists_DiscardsResults pins that the domain-interface
// method is a pass-through, so the callers that never wanted the memberships keep
// behaving exactly as they did.
func TestListService_SubscribeToLists_DiscardsResults(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	service, mockRepo, mockWorkspaceRepo, mockContactListRepo, mockContactRepo, mockAuthService := newSubscribeResultsService(ctrl)

	ctx := context.Background()
	workspaceID := "workspace123"
	email := "passthrough@example.com"

	mockAuthService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).
		Return(ctx, &domain.User{}, subscribeResultsUserWorkspace(workspaceID), nil)
	mockWorkspaceRepo.EXPECT().GetByID(gomock.Any(), workspaceID).Return(subscribeResultsWorkspace(workspaceID), nil)
	mockContactRepo.EXPECT().UpsertContact(gomock.Any(), workspaceID, gomock.Any()).Return(true, nil)
	mockRepo.EXPECT().GetLists(gomock.Any(), workspaceID).Return([]*domain.List{
		{ID: "list123", Name: "Test List"},
	}, nil)
	mockContactListRepo.EXPECT().GetContactListByIDs(gomock.Any(), workspaceID, email, "list123").
		Return(nil, &domain.ErrContactListNotFound{Message: "not found"})
	mockContactListRepo.EXPECT().AddContactToList(gomock.Any(), workspaceID, gomock.Any()).Return(nil)

	err := service.SubscribeToLists(ctx, &domain.SubscribeToListsRequest{
		WorkspaceID: workspaceID,
		Contact:     domain.Contact{Email: email},
		ListIDs:     []string{"list123"},
	}, true)
	assert.NoError(t, err)
}
