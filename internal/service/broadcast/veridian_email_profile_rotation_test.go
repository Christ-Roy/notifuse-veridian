package broadcast

import (
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestVeridianResolveEmailProfile_RoundRobinAndConcurrency(t *testing.T) {
	provider := func(id string) domain.Integration {
		verified := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		return domain.Integration{ID: id, Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, VeridianTransportVerifiedAt: &verified}}
	}
	workspace := &domain.Workspace{
		ID:           "ws",
		Settings:     domain.WorkspaceSettings{VeridianMarketingEmailProviderIDs: []string{"p1", "p2", "p3"}},
		Integrations: []domain.Integration{provider("p1"), provider("p2"), provider("p3")},
	}
	rotator := newVeridianEmailProfileRotator()

	// Stable across fresh processes/allocations for the same logical message.
	first, _ := veridianResolveEmailProfile(rotator, workspace, "", nil, domain.ProviderClassGoogle, "msg-0")
	second, _ := veridianResolveEmailProfile(newVeridianEmailProfileRotator(), workspace, "", nil, domain.ProviderClassGoogle, "msg-0")
	assert.Equal(t, first, second)

	// Fixed independent message keys cover every bucket evenly (difference <=1)
	// while repeated concurrent/retry resolution cannot move an entry.
	counts := map[string]int{}
	for _, key := range []string{"msg-0", "msg-4", "msg-7"} {
		for i := 0; i < 100; i++ {
			selected, _ := veridianResolveEmailProfile(rotator, workspace, "", nil, domain.ProviderClassGoogle, key)
			counts[selected]++
		}
	}
	assert.Equal(t, map[string]int{"p1": 100, "p2": 100, "p3": 100}, counts)
}

func TestVeridianResolveEmailProfile_LegacyFallback(t *testing.T) {
	provider := &domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}
	id, got := veridianResolveEmailProfile(nil, nil, "legacy", provider, domain.ProviderClassGoogle, "msg")
	assert.Equal(t, "legacy", id)
	assert.Same(t, provider, got)
}
