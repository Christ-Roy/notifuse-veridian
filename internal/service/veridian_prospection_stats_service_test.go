package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
)

func TestVeridianProspectionStatsService(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianProspectionStatsRepository(ctrl)
	auth := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	svc := NewVeridianProspectionStatsService(repo, auth, log)

	member := func(contacts, history bool) *domain.UserWorkspace {
		return &domain.UserWorkspace{Permissions: domain.UserPermissions{
			domain.PermissionResourceContacts:       {Read: contacts},
			domain.PermissionResourceMessageHistory: {Read: history},
		}}
	}

	t.Run("workspace requis", func(t *testing.T) {
		_, err := svc.GetProspectionStats(context.Background(), &domain.VeridianProspectionStatsRequest{})
		require.Error(t, err)
		_, err = svc.GetProspectionStats(context.Background(), nil)
		require.Error(t, err)
	})

	t.Run("authentification refusee : aucune lecture", func(t *testing.T) {
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), nil, nil, errors.New("nope"))
		_, err := svc.GetProspectionStats(context.Background(), &domain.VeridianProspectionStatsRequest{WorkspaceID: "ws1"})
		require.Error(t, err)
	})

	t.Run("lecture des contacts ET de l'historique exigee, avant toute donnee", func(t *testing.T) {
		for _, m := range []*domain.UserWorkspace{member(false, true), member(true, false)} {
			auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, m, nil)
			_, err := svc.GetProspectionStats(context.Background(), &domain.VeridianProspectionStatsRequest{WorkspaceID: "ws1"})
			var perm *domain.PermissionError
			require.ErrorAs(t, err, &perm, "403 par WriteAuthAwareError")
		}
	})

	t.Run("lecture complete", func(t *testing.T) {
		since := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, member(true, true), nil)
		repo.EXPECT().GetProspectionRaw(gomock.Any(), "ws1", since, time.Time{}).Return(&domain.VeridianProspectionRaw{
			Lists:       []domain.VeridianProspectionListRow{{ID: "l", Name: "L", Active: 10, NeverContacted: 7}},
			ListReplies: []domain.VeridianProspectionReplyRow{{Key: "l", Human: 1}},
			ListSent:    []domain.VeridianProspectionKeyCount{{Key: "l", Count: 4}},
		}, nil)
		out, err := svc.GetProspectionStats(context.Background(), &domain.VeridianProspectionStatsRequest{WorkspaceID: "ws1", Since: since})
		require.NoError(t, err)
		require.Len(t, out.Segments, 1)
		assert.Equal(t, 7, out.Totals.StockRemaining)
		require.NotNil(t, out.Segments[0].ReplyRateHuman)
		assert.InDelta(t, 0.25, *out.Segments[0].ReplyRateHuman, 1e-9)
	})

	t.Run("erreur de lecture", func(t *testing.T) {
		auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws1").Return(context.Background(), &domain.User{}, member(true, true), nil)
		repo.EXPECT().GetProspectionRaw(gomock.Any(), "ws1", gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))
		_, err := svc.GetProspectionStats(context.Background(), &domain.VeridianProspectionStatsRequest{WorkspaceID: "ws1"})
		require.Error(t, err)
	})
}

func TestNewVeridianProspectionStatsServiceRetainsDependencies(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockVeridianProspectionStatsRepository(ctrl)
	auth := mocks.NewMockAuthService(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	concrete, ok := NewVeridianProspectionStatsService(repo, auth, log).(*veridianProspectionStatsService)
	require.True(t, ok)
	assert.Same(t, repo, concrete.repo)
	assert.Same(t, auth, concrete.authService)
	assert.NotNil(t, concrete.now)
}
