package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Le cas d'ilumphotos (25/11/2026) : attach-owner vers une autre personne, un client propriétaire. Rien ne s'écrit, personne n'est créé
// (tout appel non attendu d'un simulacre fait échouer).
func TestAttachOwner_ClientOwnerProtected(t *testing.T) {
	for name, person := range map[string]*domain.User{"personne connue": {ID: "nous", Email: "nous@x.test", Type: domain.UserTypeUser}, "personne nouvelle": nil} {
		t.Run(name, func(t *testing.T) {
			svc, m := newVeridianService(t)
			ctx := context.Background()
			m.workspaceRepo.EXPECT().GetByID(ctx, "ws").Return(&domain.Workspace{ID: "ws"}, nil)
			if person != nil {
				m.user.EXPECT().GetUserByEmail(ctx, "nous@x.test").Return(person, nil)
			} else {
				m.user.EXPECT().GetUserByEmail(ctx, "nous@x.test").Return(nil, &domain.ErrUserNotFound{Message: "not found"})
			}
			m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, gomock.Any(), "ws").Return(nil, sql.ErrNoRows)
			m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws").Return([]*domain.UserWorkspaceWithEmail{
				{UserWorkspace: domain.UserWorkspace{UserID: "client", WorkspaceID: "ws", Role: "owner"}, Email: "client@x.test", Type: domain.UserTypeUser},
			}, nil)
			_, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws", OwnerEmail: "nous@x.test"})
			require.ErrorIs(t, err, ErrAnotherOwnerExists)
		})
	}
}

func TestAnotherHumanOwner(t *testing.T) {
	s, _ := newVeridianService(t)
	ours := func(m *domain.UserWorkspaceWithEmail) bool { return m != nil && m.UserID == "admin" }
	row := func(id, role, mail string, typ domain.UserType) *domain.UserWorkspaceWithEmail {
		return &domain.UserWorkspaceWithEmail{UserWorkspace: domain.UserWorkspace{UserID: id, Role: role}, Email: mail, Type: typ}
	}
	client := row("c", "owner", "c@x.test", domain.UserTypeUser)
	assert.True(t, s.anotherHumanOwner([]*domain.UserWorkspaceWithEmail{client}, "nous", ours))
	assert.False(t, s.anotherHumanOwner([]*domain.UserWorkspaceWithEmail{client}, "c", ours), "lui-même")
	for name, r := range map[string]*domain.UserWorkspaceWithEmail{
		"membre": row("c", "member", "c@x.test", domain.UserTypeUser), "machine": row("k", "owner", "k@x.test", domain.UserTypeAPIKey),
		"root": row("r", "owner", "ROOT@veridian.site", domain.UserTypeUser), "vide": nil, "administrateur": row("admin", "owner", "a@x.test", domain.UserTypeUser),
	} {
		assert.False(t, s.anotherHumanOwner([]*domain.UserWorkspaceWithEmail{r}, "nous", ours), name)
	}
}

func TestRefuseIfGone(t *testing.T) {
	defer func() { VeridianPersonGone = nil }()
	ctx := context.Background()
	assert.NoError(t, refuseIfGone(ctx, "ws", "h"), "capacité absente")
	VeridianPersonGone = func(_ context.Context, ws, hub string) (bool, error) {
		if hub == "casse" {
			return false, errors.New("base")
		}
		return ws == "ws" && hub == "parti", nil
	}
	assert.ErrorIs(t, refuseIfGone(ctx, "ws", "parti"), ErrRemovedByOwner)
	assert.NoError(t, refuseIfGone(ctx, "autre", "parti"))
	assert.NoError(t, refuseIfGone(ctx, "ws", "reste"))
	assert.Error(t, refuseIfGone(ctx, "ws", "casse"), "non lu : on ne remet personne")
}

// Trois propriétaires : l'administrateur en tête de liste n'est jamais celui que le transfert rétrograde, ni celui qu'on annonce.
func TestTransfer_DemotesTheClientNeverThePlatformAdmin(t *testing.T) {
	defer func() { VeridianPlatformAdmins = nil }()
	VeridianPlatformAdmins = func(context.Context, string) (map[string]bool, error) { return map[string]bool{"admin": true}, nil }
	svc, m := newVeridianService(t)
	ctx := context.Background()
	m.workspaceRepo.EXPECT().GetByID(ctx, "ws").Return(&domain.Workspace{ID: "ws"}, nil)
	m.user.EXPECT().GetUserByEmail(ctx, "neuf@x.test").Return(&domain.User{ID: "neuf", Email: "neuf@x.test", Type: domain.UserTypeUser}, nil)
	m.workspaceRepo.EXPECT().GetUserWorkspace(ctx, "neuf", "ws").Return(&domain.UserWorkspace{UserID: "neuf", WorkspaceID: "ws", Role: "member"}, nil)
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(ctx, "ws").Return([]*domain.UserWorkspaceWithEmail{
		{UserWorkspace: domain.UserWorkspace{UserID: "admin", WorkspaceID: "ws", Role: "owner"}, Email: "a@x.test", Type: domain.UserTypeUser},
		{UserWorkspace: domain.UserWorkspace{UserID: "client", WorkspaceID: "ws", Role: "owner"}, Email: "c@x.test", Type: domain.UserTypeUser},
	}, nil).AnyTimes()
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	demoted := ""
	m.workspace.EXPECT().TransferOwnership(gomock.Any(), "ws", "neuf", gomock.Any()).DoAndReturn(func(_ context.Context, _, _, current string) error {
		demoted = current
		return errors.New("arrêt voulu du test")
	})
	_, err := svc.AttachOwner(ctx, domain.AttachOwnerInput{TenantID: "ws", OwnerEmail: "neuf@x.test", Transfer: true})
	require.Error(t, err)
	assert.Equal(t, "client", demoted)

	VeridianPlatformAdmins = func(context.Context, string) (map[string]bool, error) { return nil, errors.New("base") }
	_, err = svc.ours(ctx, "ws")
	assert.Error(t, err, "non lu : on ne devine pas qui est le client")
}
