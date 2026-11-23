package queue

import (
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type dispatchConsumer struct {
	name string
	err  error
	got  []*domain.VeridianIMAPMessage
}

func (c *dispatchConsumer) Name() string { return c.name }
func (c *dispatchConsumer) OnNewMessage(m *domain.VeridianIMAPMessage) error {
	c.got = append(c.got, m)
	return c.err
}

func TestDispatchMessage_AllConsumersAndFailure(t *testing.T) {
	a, b := &dispatchConsumer{name: "a"}, &dispatchConsumer{name: "b"}
	s := &VeridianIMAPPollerService{logger: logger.NewLogger()}
	s.RegisterConsumer(a)
	s.RegisterConsumer(b)
	m := &domain.VeridianIMAPMessage{WorkspaceID: "ws", MessageID: "x"}
	if !s.DispatchMessage(m) || len(a.got) != 1 || len(b.got) != 1 {
		t.Fatalf("%d %d", len(a.got), len(b.got))
	}
	a.err = errors.New("panne")
	if s.DispatchMessage(m) || len(b.got) != 2 {
		t.Fatal("un consommateur en échec se signale sans priver les autres")
	}
	if s.DispatchMessage(nil) {
		t.Fatal("message absent")
	}
}
