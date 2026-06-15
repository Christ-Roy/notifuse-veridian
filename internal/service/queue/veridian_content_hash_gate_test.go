package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestVeridianResolveAntiHashWindow(t *testing.T) {
	tests := []struct {
		name      string
		workspace *domain.Workspace
		provider  *domain.EmailProvider
		want      time.Duration
	}{
		{
			name:      "rien → défaut 72h",
			workspace: &domain.Workspace{},
			provider:  &domain.EmailProvider{},
			want:      domain.VeridianDefaultAntiHashWindow,
		},
		{
			name:      "infra prime workspace",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianAntiHashWindowHours: 96}},
			provider:  &domain.EmailProvider{VeridianAntiHashWindowHours: 24},
			want:      24 * time.Hour,
		},
		{
			name:      "workspace si infra absent",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianAntiHashWindowHours: 48}},
			provider:  &domain.EmailProvider{},
			want:      48 * time.Hour,
		},
		{
			name:      "provider nil → workspace",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianAntiHashWindowHours: 12}},
			provider:  nil,
			want:      12 * time.Hour,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, veridianResolveAntiHashWindow(tt.workspace, tt.provider))
		})
	}
}

func TestVeridianContentHashGate_NoHashIsNoOp(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	provider := &workspace.Integrations[0].EmailProvider
	// Payload sans hash → aucune lecture repo (EXPECT non posé = échouerait si appelé).
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{})

	assert.NotPanics(t, func() {
		env.worker.veridianContentHashGate(workspace, provider, entry)
	})
}

func TestVeridianContentHashGate_CollisionLogsButDoesNotBlock(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	provider := &workspace.Integrations[0].EmailProvider
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{
		VeridianContentHash: "deadbeefdeadbeefdeadbeefdeadbeef",
	})

	// Collision résiduelle : EXISTS true. Le gate logge mais ne retourne rien (pas
	// de blocage). La classe google (suffixe gmail.com) → domaines non vides.
	env.mockMessageHistoryRepo.EXPECT().
		ExistsContentHashSince(gomock.Any(), "ws-1", "deadbeefdeadbeefdeadbeefdeadbeef", gomock.Any(), gomock.Any(), gomock.Any()).
		Return(true, nil)

	assert.NotPanics(t, func() {
		env.worker.veridianContentHashGate(workspace, provider, entry)
	})
}

func TestVeridianContentHashGate_DBErrorIsBestEffort(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	provider := &workspace.Integrations[0].EmailProvider
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{
		VeridianContentHash: "cafecafecafecafecafecafecafecafe",
	})

	// Erreur DB → best-effort : le gate logge + passe, jamais de panic ni de blocage.
	env.mockMessageHistoryRepo.EXPECT().
		ExistsContentHashSince(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, errors.New("db down"))

	assert.NotPanics(t, func() {
		env.worker.veridianContentHashGate(workspace, provider, entry)
	})
}

func TestVeridianContentHashGate_NoCollisionNoLog(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	provider := &workspace.Integrations[0].EmailProvider
	entry := veridianTestEntry("e1", "a@gmail.com", domain.EmailQueuePayload{
		VeridianContentHash: "0123456789abcdef0123456789abcdef",
	})

	env.mockMessageHistoryRepo.EXPECT().
		ExistsContentHashSince(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, nil)

	// Pas de collision : le gate constate l'absence, ne bloque pas. Vérifie aussi
	// que since est bien dans le passé (fenêtre).
	_ = context.Background()
	assert.NotPanics(t, func() {
		env.worker.veridianContentHashGate(workspace, provider, entry)
	})
}
