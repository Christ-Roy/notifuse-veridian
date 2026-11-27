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

// VER-270 : sur l'exemplaire qui ne mène pas, le worker d'envoi ne voit aucun espace, donc ne prend aucune entrée.
func TestEmailQueueWorker_VeridianFollowLeader(t *testing.T) {
	w := &EmailQueueWorker{workspaceRepo: leaderRepo{}}
	w.VeridianFollowLeader(leaderGate(false))
	ws, _ := w.workspaceRepo.List(context.Background())
	assert.Empty(t, ws, "suiveur : aucun envoi")
}

// VER-270 : la relève IMAP suit le leader comme l'envoi.
func TestVeridianIMAPPollerService_VeridianFollowLeader(t *testing.T) {
	p := &VeridianIMAPPollerService{workspaceRepo: leaderRepo{}}
	p.VeridianFollowLeader(leaderGate(true))
	ws, _ := p.workspaceRepo.List(context.Background())
	assert.Len(t, ws, 1, "leader : relève normale")
	p = &VeridianIMAPPollerService{workspaceRepo: leaderRepo{}}
	p.VeridianFollowLeader(leaderGate(false))
	ws, _ = p.workspaceRepo.List(context.Background())
	assert.Empty(t, ws, "suiveur : aucune relève")
}
