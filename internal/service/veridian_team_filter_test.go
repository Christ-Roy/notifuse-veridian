package service

// === Veridian patch === Tests pour le filtre Veridian-managed appliqué
// dans WorkspaceService.GetWorkspaceMembersWithEmail. Cf ticket P1
// todo/done/2026-05-18-hide-veridian-api-key.md : les users veridian-managed
// (typiquement l'api_key veridian-api-<tenant>) ne doivent PAS apparaitre
// dans Team Settings, sans pour autant casser AttachOwner qui consume
// directement le repo (pas via le service).

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func newWorkspaceServiceForVeridianTeamFilter(t *testing.T) (
	*WorkspaceService,
	*mocks.MockWorkspaceRepository,
	*mocks.MockAuthService,
	*pkgmocks.MockLogger,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockUserRepo := mocks.NewMockUserRepository(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)
	mockUserService := mocks.NewMockUserServiceInterface(ctrl)
	mockAuthService := mocks.NewMockAuthService(ctrl)

	svc := NewWorkspaceService(
		mockRepo,
		mockUserRepo,
		mocks.NewMockTaskRepository(ctrl),
		mockLogger,
		mockUserService,
		mockAuthService,
		pkgmocks.NewMockMailer(ctrl),
		&config.Config{RootEmail: "root@veridian.site"},
		mocks.NewMockContactService(ctrl),
		mocks.NewMockListService(ctrl),
		mocks.NewMockContactListService(ctrl),
		mocks.NewMockTemplateService(ctrl),
		mocks.NewMockWebhookRegistrationService(ctrl),
		"secret_key",
		&SupabaseService{},
		&DNSVerificationService{},
		&BlogService{},
	)

	return svc, mockRepo, mockAuthService, mockLogger
}

// TestWorkspaceService_GetWorkspaceMembersWithEmail_FiltersVeridianManaged
// verifie que le user api_key Veridian-managed n'apparait pas dans le listing
// renvoye au handler Team Settings. Le test passe par GetWorkspaceUsersWithEmail
// (repo direct) avec les 3 membres : owner humain + member humain + api_key
// Veridian-managed. On attend exactement 2 membres en sortie.
func TestWorkspaceService_GetWorkspaceMembersWithEmail_FiltersVeridianManaged(t *testing.T) {
	svc, repo, auth, _ := newWorkspaceServiceForVeridianTeamFilter(t)

	ctx := context.Background()
	const wsID = "ws-1"
	caller := &domain.User{ID: "owner-id", Type: domain.UserTypeUser, Email: "owner@x.test"}

	auth.EXPECT().AuthenticateUserForWorkspace(ctx, wsID).Return(ctx, caller, nil, nil)
	repo.EXPECT().GetUserWorkspace(ctx, "owner-id", wsID).Return(
		&domain.UserWorkspace{UserID: "owner-id", WorkspaceID: wsID, Role: "owner"}, nil,
	)
	repo.EXPECT().GetWorkspaceUsersWithEmail(ctx, wsID).Return([]*domain.UserWorkspaceWithEmail{
		{
			UserWorkspace: domain.UserWorkspace{UserID: "owner-id", WorkspaceID: wsID, Role: "owner"},
			Email:         "owner@x.test", Type: domain.UserTypeUser,
		},
		{
			UserWorkspace: domain.UserWorkspace{UserID: "member-id", WorkspaceID: wsID, Role: "member"},
			Email:         "member@x.test", Type: domain.UserTypeUser,
		},
		{
			UserWorkspace:   domain.UserWorkspace{UserID: "api-key-id", WorkspaceID: wsID, Role: "member"},
			Email:           "veridian-api-ws-1@notifuse.test", Type: domain.UserTypeAPIKey,
			VeridianManaged: true, // ← doit etre filtre
		},
	}, nil)
	// Invitations vides — pas de mock specifique sur la conversion suivante.
	repo.EXPECT().GetWorkspaceInvitations(ctx, wsID).Return([]*domain.WorkspaceInvitation{}, nil)

	members, err := svc.GetWorkspaceMembersWithEmail(ctx, wsID)
	require.NoError(t, err)
	require.Len(t, members, 2, "veridian-managed api_key doit etre exclu du listing UI")
	emails := []string{members[0].Email, members[1].Email}
	assert.NotContains(t, emails, "veridian-api-ws-1@notifuse.test", "api_key Veridian-managed ne doit jamais apparaitre")
	assert.Contains(t, emails, "owner@x.test")
	assert.Contains(t, emails, "member@x.test")
}

// TestWorkspaceService_GetWorkspaceMembersWithEmail_NoVeridianManaged_NoFilterEffect
// regression : sans veridian-managed users, tous les members reels passent
// (le filtre ne doit rien casser sur les workspaces non-Veridian).
func TestWorkspaceService_GetWorkspaceMembersWithEmail_NoVeridianManaged_NoFilterEffect(t *testing.T) {
	svc, repo, auth, _ := newWorkspaceServiceForVeridianTeamFilter(t)

	ctx := context.Background()
	const wsID = "ws-2"
	caller := &domain.User{ID: "owner-id", Type: domain.UserTypeUser}

	auth.EXPECT().AuthenticateUserForWorkspace(ctx, wsID).Return(ctx, caller, nil, nil)
	repo.EXPECT().GetUserWorkspace(ctx, "owner-id", wsID).Return(
		&domain.UserWorkspace{UserID: "owner-id", WorkspaceID: wsID, Role: "owner"}, nil,
	)
	repo.EXPECT().GetWorkspaceUsersWithEmail(ctx, wsID).Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "owner-id", WorkspaceID: wsID, Role: "owner"}, Email: "owner@x.test", Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "api-self", WorkspaceID: wsID, Role: "member"}, Email: "api@x.test", Type: domain.UserTypeAPIKey},
	}, nil)
	repo.EXPECT().GetWorkspaceInvitations(ctx, wsID).Return([]*domain.WorkspaceInvitation{}, nil)

	members, err := svc.GetWorkspaceMembersWithEmail(ctx, wsID)
	require.NoError(t, err)
	require.Len(t, members, 2)
}

// TestWorkspaceService_GetWorkspaceMembersWithEmail_PreservesInvitations
// verifie que le filtre n'impacte pas les invitations actives (legitimes,
// jamais marquees veridian-managed) qui sont concatenees apres le filtrage
// des membres reels.
func TestWorkspaceService_GetWorkspaceMembersWithEmail_PreservesInvitations(t *testing.T) {
	svc, repo, auth, _ := newWorkspaceServiceForVeridianTeamFilter(t)

	ctx := context.Background()
	const wsID = "ws-3"
	caller := &domain.User{ID: "owner-id", Type: domain.UserTypeUser}

	auth.EXPECT().AuthenticateUserForWorkspace(ctx, wsID).Return(ctx, caller, nil, nil)
	repo.EXPECT().GetUserWorkspace(ctx, "owner-id", wsID).Return(
		&domain.UserWorkspace{UserID: "owner-id", WorkspaceID: wsID, Role: "owner"}, nil,
	)
	repo.EXPECT().GetWorkspaceUsersWithEmail(ctx, wsID).Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "owner-id", WorkspaceID: wsID, Role: "owner"}, Email: "owner@x.test", Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "api-managed", WorkspaceID: wsID, Role: "member"}, Email: "veridian-api@notifuse.test", Type: domain.UserTypeAPIKey, VeridianManaged: true},
	}, nil)
	// Invitation active future = +30 jours, doit etre listee.
	future := time.Now().Add(30 * 24 * time.Hour)
	repo.EXPECT().GetWorkspaceInvitations(ctx, wsID).Return([]*domain.WorkspaceInvitation{
		{ID: "inv-1", WorkspaceID: wsID, Email: "invitee@x.test", ExpiresAt: future, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}, nil)

	members, err := svc.GetWorkspaceMembersWithEmail(ctx, wsID)
	require.NoError(t, err)
	// 1 owner reel (api filtre) + 1 invitation = 2 lignes.
	require.Len(t, members, 2)
	emails := []string{members[0].Email, members[1].Email}
	assert.Contains(t, emails, "owner@x.test")
	assert.Contains(t, emails, "invitee@x.test")
	assert.NotContains(t, emails, "veridian-api@notifuse.test")
}
