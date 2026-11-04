package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type verifierSpy struct {
	calls [][3]string
	err   error
}

func (v *verifierSpy) VeridianAdminVerifyEmailIntegration(_ context.Context, ws, id, to string) error {
	v.calls = append(v.calls, [3]string{ws, id, to})
	return v.err
}

func newVerifyServiceForTest(t *testing.T) (*VeridianEmailProfileVerifyService, *mocks.MockAuthService, *verifierSpy) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	auth := mocks.NewMockAuthService(ctrl)
	spy := &verifierSpy{}
	return NewVeridianEmailProfileVerifyService(auth, spy), auth, spy
}

func TestVeridianEmailProfileVerifyService_VerifyEmailProfile(t *testing.T) {
	ctx := context.Background()

	t.Run("cle en ecriture du workspace : le transport est teste dans CE workspace", func(t *testing.T) {
		svc, auth, spy := newVerifyServiceForTest(t)
		auth.EXPECT().AuthenticateUserForWorkspace(ctx, "ws").Return(ctx, &domain.User{}, authzKeyWith(true, true), nil)
		require.NoError(t, svc.VerifyEmailProfile(ctx, "ws", "p1", "me@client.fr"))
		assert.Equal(t, [][3]string{{"ws", "p1", "me@client.fr"}}, spy.calls)
	})

	t.Run("cle d'un autre workspace : refusee, le transport n'est pas touche", func(t *testing.T) {
		svc, auth, spy := newVerifyServiceForTest(t)
		auth.EXPECT().AuthenticateUserForWorkspace(ctx, "ws-b").Return(ctx, nil, nil, &domain.ErrUnauthorized{Message: "not a member"})
		err := svc.VerifyEmailProfile(ctx, "ws-b", "p1", "me@client.fr")
		var denied *domain.ErrUnauthorized
		require.ErrorAs(t, err, &denied)
		assert.Empty(t, spy.calls)
	})

	t.Run("cle en lecture seule : refusee, le transport n'est pas touche", func(t *testing.T) {
		svc, auth, spy := newVerifyServiceForTest(t)
		auth.EXPECT().AuthenticateUserForWorkspace(ctx, "ws").Return(ctx, &domain.User{}, authzKeyWith(false, true), nil)
		err := svc.VerifyEmailProfile(ctx, "ws", "p1", "me@client.fr")
		var denied *domain.ErrUnauthorized
		require.ErrorAs(t, err, &denied)
		assert.Empty(t, spy.calls)
	})

	t.Run("champs manquants : 400 avant toute authentification", func(t *testing.T) {
		svc, _, spy := newVerifyServiceForTest(t)
		err := svc.VerifyEmailProfile(ctx, "ws", "", "me@client.fr")
		var v domain.ValidationError
		require.ErrorAs(t, err, &v)
		assert.Empty(t, spy.calls)
	})

	t.Run("echec du transport : propage l'erreur", func(t *testing.T) {
		svc, auth, spy := newVerifyServiceForTest(t)
		spy.err = errors.New("smtp refused")
		auth.EXPECT().AuthenticateUserForWorkspace(ctx, "ws").Return(ctx, &domain.User{}, &domain.UserWorkspace{Role: "owner"}, nil)
		require.EqualError(t, svc.VerifyEmailProfile(ctx, "ws", "p1", "me@client.fr"), "smtp refused")
	})

	t.Run("plafond de verifications par minute et par workspace", func(t *testing.T) {
		svc, auth, spy := newVerifyServiceForTest(t)
		clock := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
		svc.now = func() time.Time { return clock }
		auth.EXPECT().AuthenticateUserForWorkspace(ctx, gomock.Any()).Return(ctx, &domain.User{}, &domain.UserWorkspace{Role: "owner"}, nil).AnyTimes()
		for i := 0; i < veridianVerifyMaxPerMinute; i++ {
			require.NoError(t, svc.VerifyEmailProfile(ctx, "ws", "p1", "me@client.fr"))
		}
		require.ErrorIs(t, svc.VerifyEmailProfile(ctx, "ws", "p1", "me@client.fr"), ErrVeridianVerifyRateLimited)
		require.NoError(t, svc.VerifyEmailProfile(ctx, "autre", "p1", "me@client.fr"), "un autre workspace a son propre compteur")
		clock = clock.Add(61 * time.Second)
		require.NoError(t, svc.VerifyEmailProfile(ctx, "ws", "p1", "me@client.fr"))
		assert.Len(t, spy.calls, veridianVerifyMaxPerMinute+2)
	})
}
