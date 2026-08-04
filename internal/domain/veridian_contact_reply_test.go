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
	marked       *VeridianContactReply
	replied      bool
	repliedCount int
}

func (s *stubReplyRepo) MarkReplied(_ context.Context, _ string, r *VeridianContactReply) error {
	// Model the repository contract: ON CONFLICT DO NOTHING, first signal wins.
	if s.marked == nil {
		s.marked = r
	}
	s.replied = true
	return nil
}
func (s *stubReplyRepo) HasReplied(_ context.Context, _ string, _ string) (bool, error) {
	return s.replied, nil
}
func (s *stubReplyRepo) CountRepliedSince(_ context.Context, _ string, _, _ time.Time) (int, error) {
	return s.repliedCount, nil
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

func TestVeridianContactReplyRepository_RetryKeepsFirstSignal(t *testing.T) {
	first := &VeridianContactReply{
		ContactEmail: "prospect@acme.fr",
		RepliedAt:    time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC),
		MatchType:    VeridianReplyMatchMessageID,
	}
	retry := &VeridianContactReply{
		ContactEmail: "prospect@acme.fr",
		RepliedAt:    first.RepliedAt.Add(time.Minute),
		MatchType:    VeridianReplyMatchSenderFallback,
	}

	stub := &stubReplyRepo{}
	assert.NoError(t, stub.MarkReplied(context.Background(), "ws", first))
	assert.NoError(t, stub.MarkReplied(context.Background(), "ws", retry))
	assert.Same(t, first, stub.marked, "an IMAP replay must not replace the first reply signal")
	assert.True(t, stub.replied)
}
