package service

// === Veridian patch — Hub discovery cross-app (2026-05-20) ===
// Tests unitaires de veridianService.LookupByEmail.
// Couvre :
//   - User connu + 2 workspaces → found:true, 2 workspaces avec plan
//   - User inconnu (sql.ErrNoRows) → found:false, workspaces vide
//   - User inconnu (message "not found") → found:false
//   - User de type api_key → found:false (exclu, non-humain)
//   - User connu + 0 workspace → found:true, workspaces vide
//   - Workspace sans plan row (legacy) → plan="" dans la reponse
//   - Erreur DB critique → erreur propagee
//   - GetUserWorkspaces erreur → erreur propagee
//   - apiEndpoint vide → fallback_url hardcoded

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupByEmail_FoundTwoWorkspaces(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "alice@example.com"
	userID := "user-alice-uuid"
	user := &domain.User{ID: userID, Email: email, Type: domain.UserTypeUser}

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(user, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), userID).Return([]*domain.UserWorkspace{
		{UserID: userID, WorkspaceID: "ws-alice", Role: "owner"},
		{UserID: userID, WorkspaceID: "ws-shared", Role: "member"},
	}, nil)

	// GetByID pour les 2 workspaces
	m.workspaceRepo.EXPECT().GetByID(gomock.Any(), "ws-alice").Return(&domain.Workspace{ID: "ws-alice", Name: "Alice Corp"}, nil)
	m.workspaceRepo.EXPECT().GetByID(gomock.Any(), "ws-shared").Return(&domain.Workspace{ID: "ws-shared", Name: "Shared Project"}, nil)

	// Plans pour les 2 workspaces
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-alice").Return(&domain.VeridianPlan{WorkspaceID: "ws-alice", Plan: "pro"}, nil)
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-shared").Return(&domain.VeridianPlan{WorkspaceID: "ws-shared", Plan: "free"}, nil)

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.True(t, resp.Found)
	assert.Equal(t, email, resp.UserEmail)
	require.Len(t, resp.Workspaces, 2)
	assert.Equal(t, "ws-alice", resp.Workspaces[0].WorkspaceID)
	assert.Equal(t, "Alice Corp", resp.Workspaces[0].WorkspaceName)
	assert.Equal(t, "owner", resp.Workspaces[0].Role)
	assert.Equal(t, "pro", resp.Workspaces[0].Plan)
	assert.True(t, resp.Workspaces[0].MagicLinkCapable)
	assert.Equal(t, "https://notifuse.app.veridian.site/console/signin", resp.Workspaces[0].FallbackURL)
	assert.Equal(t, "ws-shared", resp.Workspaces[1].WorkspaceID)
	assert.Equal(t, "member", resp.Workspaces[1].Role)
	assert.Equal(t, "free", resp.Workspaces[1].Plan)
}

func TestLookupByEmail_UserNotFound_ErrNoRows(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "ghost@example.com"
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(nil, sql.ErrNoRows)

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.False(t, resp.Found)
	assert.Equal(t, email, resp.UserEmail)
	assert.Empty(t, resp.Workspaces)
}

func TestLookupByEmail_UserNotFound_MessageNotFound(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "ghost@example.com"
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(nil, errors.New("user not found in db"))

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.False(t, resp.Found)
	assert.Empty(t, resp.Workspaces)
}

func TestLookupByEmail_APIKeyUser_Excluded(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "veridian-api-tenant@notifuse.app"
	apiKeyUser := &domain.User{ID: "apikey-uuid", Email: email, Type: domain.UserTypeAPIKey}
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(apiKeyUser, nil)
	// GetUserWorkspaces ne doit PAS etre appele (user api_key exclu).

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.False(t, resp.Found)
	assert.Empty(t, resp.Workspaces)
}

func TestLookupByEmail_UserFoundZeroWorkspaces(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "solo@example.com"
	userID := "user-solo-uuid"
	user := &domain.User{ID: userID, Email: email, Type: domain.UserTypeUser}

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(user, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), userID).Return([]*domain.UserWorkspace{}, nil)

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.True(t, resp.Found)
	assert.Empty(t, resp.Workspaces)
}

func TestLookupByEmail_WorkspaceLegacyNoPlan(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "legacy@example.com"
	userID := "user-legacy-uuid"
	user := &domain.User{ID: userID, Email: email, Type: domain.UserTypeUser}

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(user, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), userID).Return([]*domain.UserWorkspace{
		{UserID: userID, WorkspaceID: "ws-legacy", Role: "owner"},
	}, nil)
	m.workspaceRepo.EXPECT().GetByID(gomock.Any(), "ws-legacy").Return(&domain.Workspace{ID: "ws-legacy", Name: "Legacy WS"}, nil)
	// Plan absent = legacy tenant sans plan row
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-legacy").Return(nil, sql.ErrNoRows)

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.True(t, resp.Found)
	require.Len(t, resp.Workspaces, 1)
	assert.Equal(t, "", resp.Workspaces[0].Plan, "plan vide pour tenant pre-Hub sans plan row")
}

func TestLookupByEmail_DBError_Propagated(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "bob@example.com"
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(nil, errors.New("connection refused"))

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestLookupByEmail_GetUserWorkspacesError(t *testing.T) {
	svc, m := newVeridianService(t)

	email := "bob@example.com"
	userID := "user-bob-uuid"
	user := &domain.User{ID: userID, Email: email, Type: domain.UserTypeUser}

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(user, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), userID).Return(nil, errors.New("db timeout"))

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestLookupByEmail_EmptyAPIEndpoint_FallbackURL(t *testing.T) {
	// Quand apiEndpoint est vide, le fallback_url doit pointer vers le hostname
	// de prod hardcoded plutot qu'une URL malformee "/console/signin".
	svc, m := newVeridianService(t)
	svc.apiEndpoint = "" // override post-construction

	email := "alice@example.com"
	userID := "user-alice-uuid"
	user := &domain.User{ID: userID, Email: email, Type: domain.UserTypeUser}

	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), email).Return(user, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), userID).Return([]*domain.UserWorkspace{
		{UserID: userID, WorkspaceID: "ws-alice", Role: "owner"},
	}, nil)
	m.workspaceRepo.EXPECT().GetByID(gomock.Any(), "ws-alice").Return(&domain.Workspace{ID: "ws-alice", Name: "Alice"}, nil)
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-alice").Return(nil, sql.ErrNoRows)

	resp, err := svc.LookupByEmail(context.Background(), email)
	require.NoError(t, err)
	assert.True(t, resp.Found)
	assert.Equal(t, "https://notifuse.app.veridian.site/console/signin", resp.Workspaces[0].FallbackURL,
		"fallback_url doit etre hardcode quand apiEndpoint est vide")
}
