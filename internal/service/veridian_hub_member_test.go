package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Le cas qui a motivé le lot (fiche 89) : Alice (idAlice) avait old@ et porte maintenant new@ ; un compte NEUF, Bob (idBob), a repris old@. Le Hub peut
// encore envoyer old@ pour Alice. Chaque geste doit toucher Alice et jamais Bob. Vu échouer en remettant la recherche par adresse.
const (
	idAlice  = "6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01"
	idBob    = "0f9d2c11-77aa-4b8e-8f10-5f2d6a1c9b02"
	idNobody = "c1a7e0aa-3d42-4a44-9c0e-2b6f1d7e8f03"
	oldMail  = "old@example.test"
	newMail  = "new@example.test"
)

type hubWorld struct {
	users   map[string]*domain.User // par id local
	members map[string]string       // id local -> rôle dans ws-1
	removed []string
	added   []string
	events  []map[string]interface{}
}

func strp(s string) *string { return &s }

func newHubWorld(t *testing.T) (*veridianService, *veridianServiceMocks, *hubWorld) {
	t.Helper()
	svc, m, frozen := newVeridianServiceWithFrozenRepo(t)
	w := &hubWorld{
		users: map[string]*domain.User{
			"u-owner": {ID: "u-owner", Email: "owner@example.test", Type: domain.UserTypeUser, HubUserID: strp("9d9d9d9d-0000-4000-8000-000000000009")},
			"u-alice": {ID: "u-alice", Email: newMail, Type: domain.UserTypeUser, HubUserID: strp(idAlice)},
			"u-bob":   {ID: "u-bob", Email: oldMail, Type: domain.UserTypeUser, HubUserID: strp(idBob)},
			"u-avant": {ID: "u-avant", Email: "avant@example.test", Type: domain.UserTypeUser},
		},
		members: map[string]string{"u-owner": "owner", "u-alice": "member", "u-bob": "member"},
	}
	m.planRepo.EXPECT().Get(gomock.Any(), "ws-1").Return(nil, sql.ErrNoRows).AnyTimes()
	m.workspaceRepo.EXPECT().GetByID(gomock.Any(), "ws-1").Return(&domain.Workspace{ID: "ws-1"}, nil).AnyTimes()
	m.userRepo.EXPECT().GetUserByHubUserID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id string) (*domain.User, error) {
		for _, u := range w.users {
			if hubUserIDOf(u) == id {
				return u, nil
			}
		}
		return nil, &domain.ErrUserNotFound{Message: "not found"}
	}).AnyTimes()
	byMail := func(_ context.Context, email string) (*domain.User, error) {
		for _, u := range w.users {
			if u.Email == email {
				return u, nil
			}
		}
		return nil, &domain.ErrUserNotFound{Message: "user not found"}
	}
	m.user.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).DoAndReturn(byMail).AnyTimes()
	m.userRepo.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).DoAndReturn(byMail).AnyTimes()
	m.userRepo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *domain.User) error {
		w.users[u.ID] = u
		return nil
	}).AnyTimes()
	m.userRepo.EXPECT().BackfillHubUserID(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id, hub string) error {
		w.users[id].HubUserID = strp(hub)
		return nil
	}).AnyTimes()
	m.userRepo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.userRepo.EXPECT().DeleteSession(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m.workspaceRepo.EXPECT().GetUserWorkspace(gomock.Any(), gomock.Any(), "ws-1").DoAndReturn(func(_ context.Context, id, _ string) (*domain.UserWorkspace, error) {
		role, ok := w.members[id]
		if !ok {
			return nil, sql.ErrNoRows
		}
		return &domain.UserWorkspace{UserID: id, WorkspaceID: "ws-1", Role: role}, nil
	}).AnyTimes()
	m.workspaceRepo.EXPECT().GetUserWorkspaces(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	m.workspaceRepo.EXPECT().GetWorkspaceUsersWithEmail(gomock.Any(), "ws-1").Return([]*domain.UserWorkspaceWithEmail{membershipOwnerWithEmail("u-owner", "owner@example.test")}, nil).AnyTimes()
	m.workspaceRepo.EXPECT().RemoveUserFromWorkspace(gomock.Any(), gomock.Any(), "ws-1").DoAndReturn(func(_ context.Context, id, _ string) error {
		w.removed = append(w.removed, id)
		delete(w.members, id)
		return nil
	}).AnyTimes()
	m.workspace.EXPECT().AddUserToWorkspace(gomock.Any(), "ws-1", gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _, id, role string, _ domain.UserPermissions) error {
		w.added = append(w.added, id)
		w.members[id] = role
		return nil
	}).AnyTimes()
	m.emitter.EXPECT().Emit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_ context.Context, _ interface{}, _ string, data map[string]interface{}) {
		w.events = append(w.events, data)
	}).AnyTimes()
	frozen.EXPECT().Freeze(gomock.Any(), "ws-1", "u-alice", gomock.Any()).Return(&domain.VeridianFrozenMember{}, false, nil).AnyTimes()
	frozen.EXPECT().Unfreeze(gomock.Any(), "ws-1", "u-alice").Return(true, nil).AnyTimes()
	return svc, m, w
}

func TestHubMember_AddressChangedAndRecycled_EveryGestureTouchesTheRightPerson(t *testing.T) {
	ctx := context.Background()
	for _, sent := range []string{oldMail, newMail, "ailleurs@example.test", ""} {
		t.Run("adresse reçue "+sent, func(t *testing.T) {
			svc, _, w := newHubWorld(t)

			_, _, err := svc.FreezeMember(ctx, domain.FreezeMemberInput{TenantID: "ws-1", HubUserID: idAlice, UserEmail: sent})
			require.NoError(t, err)
			_, _, err = svc.UnfreezeMember(ctx, domain.UnfreezeMemberInput{TenantID: "ws-1", HubUserID: idAlice, UserEmail: sent})
			require.NoError(t, err)

			out, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{TenantID: "ws-1", HubUserID: idAlice, UserEmail: sent})
			require.NoError(t, err)
			assert.True(t, out.Removed)
			assert.Equal(t, []string{"u-alice"}, w.removed)
			assert.Equal(t, "member", w.members["u-bob"], "celui qui a repris l'adresse n'est jamais touché")
			assert.Equal(t, idAlice, w.events[len(w.events)-1]["hub_user_id"])
			assert.Equal(t, newMail, w.events[len(w.events)-1]["user_email"])

			again, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{TenantID: "ws-1", HubUserID: idAlice, UserEmail: sent})
			require.NoError(t, err)
			assert.False(t, again.Removed)

			back, err := svc.RestoreMember(ctx, domain.RestoreMemberInput{TenantID: "ws-1", HubUserID: idAlice, UserEmail: sent})
			require.NoError(t, err)
			assert.True(t, back.Restored)
			assert.Equal(t, []string{"u-alice"}, w.added)

			role, err := svc.SyncMember(ctx, domain.SyncMemberInput{TenantID: "ws-1", HubUserID: idAlice, UserEmail: sent, Role: domain.SyncMemberRoleAdmin})
			require.NoError(t, err)
			assert.Equal(t, "u-alice", role.AppUserID)

			if sent != "" { // attach-member exige l'adresse : elle sert à créer la copie
				link, err := svc.AttachMember(ctx, domain.AttachMemberInput{TenantID: "ws-1", HubUserID: idAlice, HubUserEmail: sent, Role: domain.AttachMemberRoleMember})
				require.NoError(t, err)
				assert.True(t, link.AlreadyMember)
				payload, _, _ := strings.Cut(strings.SplitN(link.LoginURL, "token=", 2)[1], ".")
				raw, decErr := base64.RawURLEncoding.DecodeString(payload)
				require.NoError(t, decErr)
				assert.Contains(t, string(raw), newMail, "le lien ouvre le compte d'Alice")
				assert.NotContains(t, string(raw), oldMail, "jamais celui qui a repris l'adresse")
			}

			assert.Len(t, w.users, 4, "aucun double")
			assert.Equal(t, []string{"u-alice"}, w.removed)
		})
	}
}

func TestHubMember_UnknownPerson_AddressNeverChoosesAnAccount(t *testing.T) {
	ctx := context.Background()
	svc, _, w := newHubWorld(t)

	out, err := svc.RemoveMember(ctx, domain.RemoveMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: oldMail})
	require.NoError(t, err)
	assert.False(t, out.Removed)
	_, err = svc.RestoreMember(ctx, domain.RestoreMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: oldMail})
	assert.ErrorIs(t, err, ErrUserNotInApp)
	_, _, err = svc.FreezeMember(ctx, domain.FreezeMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: oldMail})
	assert.ErrorIs(t, err, ErrUserNotInApp)
	_, _, err = svc.UnfreezeMember(ctx, domain.UnfreezeMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: oldMail})
	assert.ErrorIs(t, err, ErrUserNotInApp)
	_, err = svc.TransferOwner(ctx, domain.TransferOwnerInput{TenantID: "ws-1", OwnerHubUserID: idNobody, NewOwnerEmail: oldMail, Reason: "essai"})
	assert.ErrorIs(t, err, ErrUserNotInApp)

	// Une adresse déjà portée par un autre compte relié : refus, jamais une fusion, même attestée.
	for _, proven := range []bool{false, true} {
		_, err = svc.SyncMember(ctx, domain.SyncMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: oldMail, EmailProven: proven, Role: domain.SyncMemberRoleMember})
		assert.ErrorIs(t, err, ErrHubUserIDConflict)
		_, err = svc.AttachMember(ctx, domain.AttachMemberInput{TenantID: "ws-1", HubUserID: idNobody, HubUserEmail: oldMail, EmailProven: proven, Role: domain.AttachMemberRoleMember})
		assert.ErrorIs(t, err, ErrHubUserIDConflict)
	}
	assert.Empty(t, w.removed)
	assert.Empty(t, w.added)
	assert.Equal(t, idBob, hubUserIDOf(w.users["u-bob"]))
}

func TestHubMember_AccountWithoutLink_BoundOnlyWhenTheAddressIsProven(t *testing.T) {
	ctx := context.Background()
	in := domain.SyncMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: "avant@example.test", Role: domain.SyncMemberRoleMember}

	svc, _, w := newHubWorld(t)
	_, err := svc.SyncMember(ctx, in)
	assert.ErrorIs(t, err, ErrHubUserIDConflict)
	assert.Empty(t, hubUserIDOf(w.users["u-avant"]))
	assert.Empty(t, w.added)

	in.EmailProven = true
	out, err := svc.SyncMember(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "u-avant", out.AppUserID)
	assert.Equal(t, idNobody, hubUserIDOf(w.users["u-avant"]))
	assert.Len(t, w.users, 4)
}

func TestHubMember_NewPerson_IsCreatedBound(t *testing.T) {
	svc, _, w := newHubWorld(t)
	out, err := svc.SyncMember(context.Background(), domain.SyncMemberInput{TenantID: "ws-1", HubUserID: idNobody, UserEmail: "neuf@example.test", Role: domain.SyncMemberRoleMember})
	require.NoError(t, err)
	assert.Equal(t, idNobody, hubUserIDOf(w.users[out.AppUserID]))
}

func TestHubMember_TransferOwner_OnlyToAMember(t *testing.T) {
	svc, _, w := newHubWorld(t)
	delete(w.members, "u-alice")
	_, err := svc.TransferOwner(context.Background(), domain.TransferOwnerInput{TenantID: "ws-1", OwnerHubUserID: idAlice, Reason: "essai"})
	assert.ErrorIs(t, err, ErrOwnerNotMember)
}

func TestHubMember_LookupMember_ReadsTheIDNotTheAddress(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newHubWorld(t)
	out, err := svc.LookupMember(ctx, idAlice)
	require.NoError(t, err)
	assert.True(t, out.Found)
	assert.Equal(t, newMail, out.UserEmail)
	none, err := svc.LookupMember(ctx, idNobody)
	require.NoError(t, err)
	assert.False(t, none.Found)
	assert.NotNil(t, none.Workspaces)
}

func TestHubOwnerMatches(t *testing.T) {
	alice := &domain.User{Email: newMail, HubUserID: strp(idAlice)}
	assert.True(t, hubOwnerMatches(alice, idAlice, oldMail), "même personne, autre adresse")
	assert.False(t, hubOwnerMatches(alice, idBob, newMail), "autre personne, même adresse : owner_mismatch")
	assert.True(t, hubOwnerMatches(alice, "", "NEW@example.test "), "sans hub_user_id, l'adresse")
	assert.True(t, hubOwnerMatches(&domain.User{Email: newMail}, idAlice, newMail), "compte sans lien")
}

func TestHubOwner_AddressOfAnotherBoundAccountIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newHubWorld(t)
	_, err := svc.hubOwner(ctx, idNobody, oldMail)
	assert.ErrorIs(t, err, ErrHubUserIDConflict)
	owner, err := svc.hubOwner(ctx, idAlice, oldMail)
	require.NoError(t, err)
	assert.Equal(t, "u-alice", owner.ID)
	owner, err = svc.hubOwner(ctx, "", oldMail)
	require.NoError(t, err)
	assert.Equal(t, "u-bob", owner.ID)
	owner, err = svc.hubOwner(ctx, idNobody, "neuf@example.test")
	require.NoError(t, err)
	assert.Nil(t, owner)
}

func TestMagicLinkMember(t *testing.T) {
	ctx := context.Background()
	svc, m, w := newHubWorld(t)
	u, err := svc.magicLinkMember(ctx, "ws-1", idBob, newMail)
	require.NoError(t, err)
	assert.Equal(t, "u-bob", u.ID, "l'identifiant, pas l'adresse d'Alice")
	// Une clé API est un compte machine : aucun lien de connexion, même membre.
	w.users["u-key"] = &domain.User{ID: "u-key", Email: "cle@example.test", Type: domain.UserTypeAPIKey}
	w.members["u-key"] = "member"
	var notFound *domain.ErrUserNotFound
	_, err = svc.magicLinkMember(ctx, "ws-1", "", "cle@example.test")
	assert.ErrorAs(t, err, &notFound)
	// Une panne de lecture n'est pas « pas membre ».
	m.workspaceRepo.EXPECT().GetUserWorkspace(gomock.Any(), "u-bob", "ws-2").Return(nil, assert.AnError)
	_, err = svc.magicLinkMember(ctx, "ws-2", idBob, "")
	require.ErrorIs(t, err, assert.AnError)
}
