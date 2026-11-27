package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/domain"
)

type webhookLeaderRepo struct{ domain.WorkspaceRepository }

func (webhookLeaderRepo) List(context.Context) ([]*domain.Workspace, error) {
	return []*domain.Workspace{{ID: "ilumphotos"}}, nil
}

type webhookGate bool

func (g webhookGate) Leads() bool { return bool(g) }

// VER-270 : les livraisons de webhooks sont prises sans verrou ; seul l'exemplaire qui mène les voit.
func TestWebhookDeliveryWorker_VeridianFollowLeader(t *testing.T) {
	w := &WebhookDeliveryWorker{workspaceRepo: webhookLeaderRepo{}}
	w.VeridianFollowLeader(webhookGate(false))
	ws, _ := w.workspaceRepo.List(context.Background())
	assert.Empty(t, ws)
	w = &WebhookDeliveryWorker{workspaceRepo: webhookLeaderRepo{}}
	w.VeridianFollowLeader(webhookGate(true))
	ws, _ = w.workspaceRepo.List(context.Background())
	assert.Len(t, ws, 1)
}
