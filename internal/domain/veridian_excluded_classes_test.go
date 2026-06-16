package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianExcludedProviderClassesFromMetadata(t *testing.T) {
	t.Run("nil metadata returns nil", func(t *testing.T) {
		assert.Nil(t, VeridianExcludedProviderClassesFromMetadata(nil))
	})

	t.Run("key absent returns nil", func(t *testing.T) {
		assert.Nil(t, VeridianExcludedProviderClassesFromMetadata(MapOfAny{"other": []any{"google"}}))
	})

	t.Run("[]any from JSON round-trip retained, normalized", func(t *testing.T) {
		out := VeridianExcludedProviderClassesFromMetadata(MapOfAny{
			VeridianExcludedProviderClassesMetadataKey: []any{"microsoft", "google"},
		})
		assert.ElementsMatch(t, []string{"microsoft", "google"}, out)
	})

	t.Run("[]string also accepted", func(t *testing.T) {
		out := VeridianExcludedProviderClassesFromMetadata(MapOfAny{
			VeridianExcludedProviderClassesMetadataKey: []string{"microsoft"},
		})
		assert.Equal(t, []string{"microsoft"}, out)
	})

	t.Run("invalid classes dropped, case-insensitive, deduped", func(t *testing.T) {
		out := VeridianExcludedProviderClassesFromMetadata(MapOfAny{
			VeridianExcludedProviderClassesMetadataKey: []any{
				"  Microsoft ", "gmail" /* invalid */, "microsoft" /* dup */, "ovh", 42 /* non-string */, "",
			},
		})
		assert.ElementsMatch(t, []string{"microsoft", "ovh"}, out)
	})

	t.Run("only invalid classes returns nil (preserves omitempty semantics)", func(t *testing.T) {
		out := VeridianExcludedProviderClassesFromMetadata(MapOfAny{
			VeridianExcludedProviderClassesMetadataKey: []any{"gmail", "hotmail", ""},
		})
		assert.Nil(t, out)
	})

	t.Run("wrong type returns nil", func(t *testing.T) {
		assert.Nil(t, VeridianExcludedProviderClassesFromMetadata(MapOfAny{
			VeridianExcludedProviderClassesMetadataKey: "microsoft",
		}))
	})
}

func TestVeridianResolveExcludedClasses(t *testing.T) {
	t.Run("all empty returns nil (no-op strict)", func(t *testing.T) {
		ws := &Workspace{}
		provider := &EmailProvider{}
		entry := &EmailQueueEntry{}
		assert.Nil(t, VeridianResolveExcludedClasses(ws, provider, entry))
	})

	t.Run("nil everything does not panic, returns nil", func(t *testing.T) {
		assert.Nil(t, VeridianResolveExcludedClasses(nil, nil, nil))
	})

	t.Run("broadcast payload wins over infra and workspace", func(t *testing.T) {
		ws := &Workspace{Settings: WorkspaceSettings{VeridianExcludedProviderClasses: []string{"google"}}}
		provider := &EmailProvider{VeridianExcludedProviderClasses: []string{"ovh"}}
		entry := &EmailQueueEntry{Payload: EmailQueuePayload{VeridianExcludedProviderClasses: []string{"microsoft"}}}
		set := VeridianResolveExcludedClasses(ws, provider, entry)
		assert.Equal(t, map[string]bool{"microsoft": true}, set)
	})

	t.Run("infra wins over workspace when broadcast empty", func(t *testing.T) {
		ws := &Workspace{Settings: WorkspaceSettings{VeridianExcludedProviderClasses: []string{"google"}}}
		provider := &EmailProvider{VeridianExcludedProviderClasses: []string{"microsoft", "yahoo_aol"}}
		entry := &EmailQueueEntry{}
		set := VeridianResolveExcludedClasses(ws, provider, entry)
		assert.Equal(t, map[string]bool{"microsoft": true, "yahoo_aol": true}, set)
	})

	t.Run("workspace used when broadcast and infra empty", func(t *testing.T) {
		ws := &Workspace{Settings: WorkspaceSettings{VeridianExcludedProviderClasses: []string{"microsoft"}}}
		set := VeridianResolveExcludedClasses(ws, &EmailProvider{}, &EmailQueueEntry{})
		assert.Equal(t, map[string]bool{"microsoft": true}, set)
	})

	t.Run("nil provider skips infra level", func(t *testing.T) {
		ws := &Workspace{Settings: WorkspaceSettings{VeridianExcludedProviderClasses: []string{"microsoft"}}}
		set := VeridianResolveExcludedClasses(ws, nil, &EmailQueueEntry{})
		assert.Equal(t, map[string]bool{"microsoft": true}, set)
	})

	t.Run("defense in depth: bad casing in persisted blob still resolved", func(t *testing.T) {
		provider := &EmailProvider{VeridianExcludedProviderClasses: []string{" Microsoft "}}
		set := VeridianResolveExcludedClasses(nil, provider, &EmailQueueEntry{})
		assert.Equal(t, map[string]bool{"microsoft": true}, set)
	})

	t.Run("defense in depth: only-invalid persisted blob returns nil", func(t *testing.T) {
		provider := &EmailProvider{VeridianExcludedProviderClasses: []string{"gmail"}}
		set := VeridianResolveExcludedClasses(nil, provider, &EmailQueueEntry{})
		assert.Nil(t, set)
	})
}

// TestVeridianApplyProviderThrottlePropagatesExclusion couvre la propagation
// broadcast→payload de l'exclusion ajoutée dans VeridianApplyProviderThrottle.
func TestVeridianApplyProviderThrottlePropagatesExclusion(t *testing.T) {
	t.Run("excluded classes copied into payload from metadata", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{
			ID:       "bex1",
			Metadata: MapOfAny{VeridianExcludedProviderClassesMetadataKey: []any{"microsoft"}},
		}
		VeridianApplyProviderThrottle(entry, b, nil)
		assert.Equal(t, []string{"microsoft"}, entry.Payload.VeridianExcludedProviderClasses)
	})

	t.Run("no exclusion metadata leaves payload exclusion nil", func(t *testing.T) {
		entry := &EmailQueueEntry{}
		b := &Broadcast{ID: "bex2", Metadata: MapOfAny{}}
		VeridianApplyProviderThrottle(entry, b, nil)
		assert.Nil(t, entry.Payload.VeridianExcludedProviderClasses)
	})
}
