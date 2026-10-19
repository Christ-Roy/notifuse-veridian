package service

import (
	"context"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func TestVeridianRequireOwner(t *testing.T) {
	human := &domain.User{ID: "u", Type: domain.UserTypeUser}
	assert.NoError(t, veridianRequireOwner(human, &domain.UserWorkspace{Role: "owner"}, "x"))
	for name, c := range map[string]struct {
		u  *domain.User
		uw *domain.UserWorkspace
	}{
		"member":       {human, &domain.UserWorkspace{Role: "member", Permissions: domain.FullPermissions}},
		"nil uw":       {human, nil},
		"nil user":     {nil, &domain.UserWorkspace{Role: "owner"}},
		"api key user": {&domain.User{ID: "k", Type: domain.UserTypeAPIKey}, &domain.UserWorkspace{Role: "owner"}},
	} {
		err := veridianRequireOwner(c.u, c.uw, "invite members")
		var unauth *domain.ErrUnauthorized
		require.Error(t, err, name)
		assert.True(t, errors.As(err, &unauth), name)
	}
}

func newOwnerGuardWorkspaceService(t *testing.T) (*WorkspaceService, *mocks.MockWorkspaceRepository, *mocks.MockAuthService) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockWorkspaceRepository(ctrl)
	auth := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	log.EXPECT().Info(gomock.Any()).AnyTimes()
	s := &WorkspaceService{repo: repo, authService: auth, logger: log}
	return s, repo, auth
}

// Cle API (role member, toutes permissions) : refus, AUCUNE ecriture (aucun
// appel repo hors auth : gomock echoue sur tout appel inattendu).
func TestWorkspaceService_InviteMember_MemberKeyForbidden(t *testing.T) {
	s, _, auth := newOwnerGuardWorkspaceService(t)
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws").
		Return(context.Background(), &domain.User{ID: "k"}, &domain.UserWorkspace{Role: "member", Permissions: domain.FullPermissions}, nil)
	_, _, err := s.InviteMember(context.Background(), "ws", "a@b.co", domain.UserPermissions{})
	var unauth *domain.ErrUnauthorized
	require.True(t, errors.As(err, &unauth), "%v", err)
}

func TestWorkspaceService_DeleteInvitation_MemberForbidden(t *testing.T) {
	s, repo, auth := newOwnerGuardWorkspaceService(t)
	auth.EXPECT().AuthenticateUserFromContext(gomock.Any()).Return(&domain.User{ID: "k"}, nil)
	repo.EXPECT().GetInvitationByID(gomock.Any(), "inv").Return(&domain.WorkspaceInvitation{ID: "inv", WorkspaceID: "ws"}, nil)
	repo.EXPECT().GetUserWorkspace(gomock.Any(), "k", "ws").Return(&domain.UserWorkspace{Role: "member", Permissions: domain.FullPermissions}, nil)
	// pas de DeleteInvitation attendu : un appel ferait echouer le test
	err := s.DeleteInvitation(context.Background(), "inv")
	var unauth *domain.ErrUnauthorized
	require.True(t, errors.As(err, &unauth), "%v", err)
}
