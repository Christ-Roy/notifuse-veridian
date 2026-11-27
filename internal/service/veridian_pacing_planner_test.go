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

func TestVeridianPaceFromOverview(t *testing.T) {
	n := func(v int) *int { return &v }
	ov := &domain.VeridianEmailProfilesOverview{Profiles: []domain.VeridianEmailProfileOverview{
		{IntegrationID: "borne", Plan: domain.VeridianEffectivePlan{Capacity: domain.VeridianPlanCapacity{Applicable: true, RemainingUseful: n(157), WindowMinutesLeft: 435}}},
		{IntegrationID: "illimite", Plan: domain.VeridianEffectivePlan{Capacity: domain.VeridianPlanCapacity{Applicable: true, RemainingUseful: nil, WindowMinutesLeft: 435}}},
		{IntegrationID: "transac", Plan: domain.VeridianEffectivePlan{Capacity: domain.VeridianPlanCapacity{Applicable: false}}},
	}}
	got := veridianPaceFromOverview(ov)
	assert.Equal(t, 157, got["borne"].Remaining)
	assert.True(t, got["borne"].Known)
	assert.Equal(t, 435.0, got["borne"].MinutesLeft)
	assert.False(t, got["illimite"].Known, "capacité sans borne : pas d'étalement")
	assert.False(t, got["transac"].Known)
	assert.Empty(t, veridianPaceFromOverview(nil))
	assert.Nil(t, NewVeridianPacingPlanner(nil), "un service étranger ne branche rien")
}

// Un espace illisible rend une ERREUR, jamais un plan vide : la porte d'étalement laisse alors passer (mesure
// absente), au lieu de lire « rien à envoyer ».
func TestVeridianPacingPlanner_ProfilePace_UnreadableWorkspaceIsAnError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	workspaces := mocks.NewMockWorkspaceRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()
	workspaces.EXPECT().GetByID(gomock.Any(), "ws1").Return(nil, errors.New("base injoignable"))
	svc := NewVeridianEmailProfileOverviewService(&overviewRepoStub{}, mocks.NewMockMessageHistoryRepository(ctrl), workspaces, mocks.NewMockAuthService(ctrl), log)
	pace, err := NewVeridianPacingPlanner(svc).ProfilePace(context.Background(), "ws1")
	require.Error(t, err)
	assert.Nil(t, pace)
}
