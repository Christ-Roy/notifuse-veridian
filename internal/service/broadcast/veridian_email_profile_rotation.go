package broadcast

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianEmailProfileRotator is a stateless deterministic selector for complete
// email integrations. The historical name is kept local to minimize wiring;
// unlike sender round-robin it owns no process-local cursor.
type veridianEmailProfileRotator struct{}

func newVeridianEmailProfileRotator() *veridianEmailProfileRotator {
	return &veridianEmailProfileRotator{}
}

func (r *veridianEmailProfileRotator) next(key string, size int) int {
	if r == nil || size <= 1 {
		return 0
	}
	digest := sha256.Sum256([]byte(key))
	return int(binary.BigEndian.Uint64(digest[:8]) % uint64(size))
}

// veridianResolveEmailProfile selects a full integration from the workspace's
// explicit marketing pool. Stable hashing by workspace, final recipient class
// and logical message keeps retries/restarts on the same Gmail profile.
func veridianResolveEmailProfile(
	rotator *veridianEmailProfileRotator,
	workspace *domain.Workspace,
	defaultIntegrationID string,
	defaultProvider *domain.EmailProvider,
	recipientClass string,
	selectionKey string,
) (string, *domain.EmailProvider) {
	if workspace == nil {
		return defaultIntegrationID, defaultProvider
	}
	profiles := workspace.VeridianMarketingEmailProfiles()
	if len(profiles) == 0 {
		return defaultIntegrationID, defaultProvider
	}
	// Deterministic assignment survives process restarts and multiple workers.
	// The queue entry still freezes the resulting IntegrationID permanently.
	idx := rotator.next(workspace.ID+"|"+recipientClass+"|"+selectionKey, len(profiles))
	return profiles[idx].IntegrationID, profiles[idx].Provider
}
