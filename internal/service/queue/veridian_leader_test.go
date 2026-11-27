package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/domain"
)

type leaderRepo struct{ domain.WorkspaceRepository }

func (leaderRepo) List(context.Context) ([]*domain.Workspace, error) {
	return []*domain.Workspace{{ID: "ilumphotos"}}, nil
}

type leaderGate bool

func (g leaderGate) Leads() bool { return bool(g) }

// VER-270 : sur l'exemplaire qui ne mène pas, le worker d'envoi et la relève IMAP ne voient aucun espace.
func TestVeridianFollowLeader_WorkerEtReleveIMAP(t *testing.T) {
	ctx := context.Background()
	w := &EmailQueueWorker{workspaceRepo: leaderRepo{}}
	w.VeridianFollowLeader(leaderGate(false))
	ws, _ := w.workspaceRepo.List(ctx)
	assert.Empty(t, ws, "suiveur : aucun envoi")

	p := &VeridianIMAPPollerService{workspaceRepo: leaderRepo{}}
	p.VeridianFollowLeader(leaderGate(true))
	ws, _ = p.workspaceRepo.List(ctx)
	assert.Len(t, ws, 1, "leader : relève normale")
}
