package queue

import (
	"context"
	"sync"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

type recordingObserver struct {
	mu    sync.Mutex
	calls []string
}

func (o *recordingObserver) OnSent(ws *domain.Workspace, e *domain.EmailQueueEntry) {
	o.mu.Lock()
	o.calls = append(o.calls, ws.ID+"/"+e.MessageID)
	o.mu.Unlock()
}

func TestSetVeridianSentObserver_SetsAndClears(t *testing.T) {
	defer SetVeridianSentObserver(nil)
	obs := &recordingObserver{}
	ws := &domain.Workspace{ID: "ws1"}
	e := &domain.EmailQueueEntry{MessageID: "m1"}

	veridianObserveSent(ws, e) // aucun observateur : sans effet
	assert.Empty(t, obs.calls)

	SetVeridianSentObserver(obs)
	veridianObserveSent(ws, e)
	assert.Equal(t, []string{"ws1/m1"}, obs.calls)

	// entrées incomplètes : jamais d'appel
	veridianObserveSent(nil, e)
	veridianObserveSent(ws, nil)
	assert.Len(t, obs.calls, 1)

	SetVeridianSentObserver(nil)
	veridianObserveSent(ws, e)
	assert.Len(t, obs.calls, 1)
}

type recordingSyncer struct {
	mu        sync.Mutex
	workspace []string
	boxes     []string
}

func (s *recordingSyncer) SyncWorkspace(_ context.Context, ws *domain.Workspace) {
	s.mu.Lock()
	s.workspace = append(s.workspace, ws.ID)
	s.mu.Unlock()
}
func (s *recordingSyncer) SyncBox(_ context.Context, ws *domain.Workspace, in *domain.Integration) {
	s.mu.Lock()
	s.boxes = append(s.boxes, ws.ID+"/"+in.ID)
	s.mu.Unlock()
}

func TestRegisterMailboxSyncer_SetsReplacesAndClears(t *testing.T) {
	p := &VeridianIMAPPollerService{}
	assert.Nil(t, p.currentMailSyncer())
	a, b := &recordingSyncer{}, &recordingSyncer{}
	p.RegisterMailboxSyncer(a)
	assert.Same(t, a, p.currentMailSyncer())
	p.RegisterMailboxSyncer(b)
	assert.Same(t, b, p.currentMailSyncer())
	p.RegisterMailboxSyncer(nil)
	assert.Nil(t, p.currentMailSyncer())
}
