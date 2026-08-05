package domain

import (
	"fmt"
	"strings"
)

// VeridianEmailProfile is one eligible marketing transport. IntegrationID is
// the stable profile identity used by queue payloads, quota counters and logs.
type VeridianEmailProfile struct {
	IntegrationID string
	Provider      *EmailProvider
}

// VeridianMarketingEmailProfiles resolves the configured pool in declared
// order. Invalid, duplicate and non-email IDs are ignored defensively. An empty
// explicit pool falls back to the upstream singleton setting.
func (w *Workspace) VeridianMarketingEmailProfiles() []VeridianEmailProfile {
	if w == nil {
		return nil
	}
	ids := w.Settings.VeridianMarketingEmailProviderIDs
	explicitPool := len(ids) > 0
	if len(ids) == 0 && strings.TrimSpace(w.Settings.MarketingEmailProviderID) != "" {
		ids = []string{w.Settings.MarketingEmailProviderID}
	}
	profiles := make([]VeridianEmailProfile, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		integration := w.GetIntegrationByID(id)
		if integration == nil || integration.Type != IntegrationTypeEmail || integration.EmailProvider.Kind == "" ||
			(explicitPool && integration.EmailProvider.VeridianTransportVerifiedAt == nil && id != w.Settings.MarketingEmailProviderID) {
			continue
		}
		seen[id] = struct{}{}
		profiles = append(profiles, VeridianEmailProfile{IntegrationID: id, Provider: &integration.EmailProvider})
	}
	return profiles
}

// ValidateVeridianMarketingEmailProfiles rejects dangling and duplicate IDs at
// the API/model boundary instead of silently degrading to a different sender.
func (w *Workspace) ValidateVeridianMarketingEmailProfiles() error {
	if w == nil || len(w.Settings.VeridianMarketingEmailProviderIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(w.Settings.VeridianMarketingEmailProviderIDs))
	for _, rawID := range w.Settings.VeridianMarketingEmailProviderIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return fmt.Errorf("profile integration ID is required")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate profile integration ID: %s", id)
		}
		integration := w.GetIntegrationByID(id)
		if integration == nil || integration.Type != IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
			return fmt.Errorf("email profile integration not found: %s", id)
		}
		// Grandfather exactly the already-active singleton when migrating to a
		// pool. Every additional/new profile still needs a successful test.
		if integration.EmailProvider.VeridianTransportVerifiedAt == nil && id != w.Settings.MarketingEmailProviderID {
			return fmt.Errorf("email profile transport is not verified: %s", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}
