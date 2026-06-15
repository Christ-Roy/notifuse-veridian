package domain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// stubReplyRepo : implémentation triviale pour prouver que l'interface est
// satisfiable et que le contrat (signatures) est stable. Pas de DB ici — le repo
// Postgres réel est testé dans internal/repository.
type stubReplyRepo struct {
	marked  *VeridianContactReply
	replied bool
}

func (s *stubReplyRepo) MarkReplied(_ context.Context, _ string, r *VeridianContactReply) error {
	s.marked = r
	return nil
}
func (s *stubReplyRepo) HasReplied(_ context.Context, _ string, _ string) (bool, error) {
	return s.replied, nil
}

func TestVeridianContactReplyRepository_InterfaceSatisfied(t *testing.T) {
	var _ VeridianContactReplyRepository = (*stubReplyRepo)(nil)
}

func TestVeridianContactReply_Fields(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	r := &VeridianContactReply{
		ContactEmail:     "prospect@acme.fr",
		RepliedAt:        now,
		MatchType:        VeridianReplyMatchMessageID,
		MatchedMessageID: "msg-1",
	}

	stub := &stubReplyRepo{}
	require := assert.New(t)
	require.NoError(stub.MarkReplied(context.Background(), "ws", r))
	require.Same(r, stub.marked)
	require.Equal("prospect@acme.fr", stub.marked.ContactEmail)
	require.Equal(now, stub.marked.RepliedAt)
	require.Equal(VeridianReplyMatchMessageID, stub.marked.MatchType)
	require.Equal("msg-1", stub.marked.MatchedMessageID)

	stub.replied = true
	ok, err := stub.HasReplied(context.Background(), "ws", "prospect@acme.fr")
	require.NoError(err)
	require.True(ok)
}
