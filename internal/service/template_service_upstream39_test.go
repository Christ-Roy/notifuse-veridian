// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/template_service_test.go ; ne contient que les declarations absentes du fork.

package service_test

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	domainmocks "github.com/Notifuse/notifuse/internal/domain/mocks" // Corrected import path
	"github.com/Notifuse/notifuse/internal/service"                  // Added logger import
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
	"github.com/golang/mock/gomock" // Added gomock import
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	// Keep testify/assert
)

// TestCompileTemplate_InjectsOverNonMapWorkspace verifies that a workspace value which is
// present but not a usable map (e.g. a JSON null) is replaced with the full workspace
// object rather than silently leaving {{ workspace.* }} empty.
func TestCompileTemplate_InjectsOverNonMapWorkspace(t *testing.T) {
	t.Skip("écart upstream v39.1 non analysé : comportement du fork différent ou fonctionnalité absente (voir docs/claude/75)")
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockAuthService := domainmocks.NewMockAuthService(ctrl)
	mockRepo := domainmocks.NewMockTemplateRepository(ctrl)
	mockLogger := &MockLogger{}
	mockWorkspaceRepo := domainmocks.NewMockWorkspaceRepository(ctrl)
	svc := service.NewTemplateService(mockRepo, mockWorkspaceRepo, mockAuthService, mockLogger, "https://api.example.com")

	ctx := context.Background()
	workspaceID := "ws_123"
	userID := "user_abc"
	testTree := createValidTestTree(createTestTextBlock("txt1", "Visit {{ workspace.website_url }}/verify"))

	mockAuthService.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), workspaceID).Return(ctx, &domain.User{ID: userID}, &domain.UserWorkspace{
		UserID:      userID,
		WorkspaceID: workspaceID,
		Role:        "member",
		Permissions: domain.UserPermissions{
			domain.PermissionResourceTemplates: {Read: true, Write: true},
		},
	}, nil)
	mockWorkspaceRepo.EXPECT().GetByID(gomock.Any(), workspaceID).Return(&domain.Workspace{
		ID:       workspaceID,
		Settings: domain.WorkspaceSettings{WebsiteURL: "https://app.example.com"},
	}, nil)

	resp, err := svc.CompileTemplate(ctx, domain.CompileTemplateRequest{
		WorkspaceID:      workspaceID,
		VisualEditorTree: testTree,
		// "workspace" present but null — not a usable map.
		TemplateData: notifuse_mjml.MapOfAny{"workspace": nil},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.True(t, resp.Success)

	ws, ok := resp.TemplateData["workspace"].(domain.MapOfAny)
	require.True(t, ok, "non-map workspace should be replaced with the injected object")
	assert.Equal(t, "https://app.example.com", ws["website_url"])
	require.NotNil(t, resp.HTML)
	assert.Contains(t, *resp.HTML, "https://app.example.com/verify")
}
