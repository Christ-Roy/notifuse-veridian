package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReplyProcessor capture les appels ProcessInboundMessage.
type fakeReplyProcessor struct {
	calls   int
	lastMsg *domain.VeridianIMAPMessage
	lastCtx context.Context
	err     error
}

func (f *fakeReplyProcessor) ProcessInboundMessage(ctx context.Context, msg *domain.VeridianIMAPMessage) error {
	f.calls++
	f.lastMsg = msg
	f.lastCtx = ctx
	return f.err
}

func TestVeridianReplyConsumer_ImplementsContract(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	c := NewVeridianReplyConsumer(&fakeReplyProcessor{}, setupMockLogger(ctrl))
	// Doit satisfaire le contrat VeridianIMAPConsumer du poller Lot 1.
	var _ domain.VeridianIMAPConsumer = c
	assert.Equal(t, "stop-on-reply", c.Name())
}

func TestVeridianReplyConsumer_OnNewMessage_Delegates(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	proc := &fakeReplyProcessor{}
	c := NewVeridianReplyConsumer(proc, setupMockLogger(ctrl))

	msg := &domain.VeridianIMAPMessage{WorkspaceID: "ws1", From: "p@acme.fr"}
	err := c.OnNewMessage(msg)
	require.NoError(t, err)
	assert.Equal(t, 1, proc.calls)
	assert.Same(t, msg, proc.lastMsg)
}

// TestVeridianReplyConsumer_OnNewMessage_BoundsContext : le consumer DOIT borner
// le traitement par un timeout (le poller appelle OnNewMessage sans contexte et
// de façon SYNCHRONE ; un ProcessInboundMessage qui traîne sur une DB lente
// bloquerait sa goroutine indéfiniment). On vérifie que le contexte transmis a
// bien un deadline (pas un context.Background() nu).
func TestVeridianReplyConsumer_OnNewMessage_BoundsContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	proc := &fakeReplyProcessor{}
	c := NewVeridianReplyConsumer(proc, setupMockLogger(ctrl))

	require.NoError(t, c.OnNewMessage(&domain.VeridianIMAPMessage{WorkspaceID: "ws1"}))
	require.NotNil(t, proc.lastCtx)
	deadline, ok := proc.lastCtx.Deadline()
	require.True(t, ok, "le contexte transmis doit avoir un deadline (timeout), pas un Background nu")
	assert.False(t, deadline.IsZero())
}

func TestVeridianReplyConsumer_OnNewMessage_NilMessage_NoOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	proc := &fakeReplyProcessor{}
	c := NewVeridianReplyConsumer(proc, setupMockLogger(ctrl))

	require.NoError(t, c.OnNewMessage(nil))
	assert.Equal(t, 0, proc.calls)
}

func TestVeridianReplyConsumer_OnNewMessage_PropagatesError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	proc := &fakeReplyProcessor{err: errors.New("boom")}
	c := NewVeridianReplyConsumer(proc, setupMockLogger(ctrl))

	// L'erreur remonte au poller (qui logge) ; le poller marque vu quoi qu'il arrive.
	err := c.OnNewMessage(&domain.VeridianIMAPMessage{WorkspaceID: "ws1"})
	require.Error(t, err)
}
