package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Tests pour les types Lot K (CONTRAT-HUB §5.15 + §5.16) ===

func TestAPIKeyGracePeriod_Is5Minutes(t *testing.T) {
	// CONTRAT-HUB §5.15 — la grace period DOIT etre 5 minutes. Ce test fige
	// la constante pour qu'un changement accidentel se manifeste.
	assert.Equal(t, 5*time.Minute, APIKeyGracePeriod)
}

func TestEventTenantAPIKeyRotated_Value(t *testing.T) {
	// Event name est consomme par le Hub — figer la valeur pour qu'un rename
	// accidentel casse les tests.
	assert.Equal(t, VeridianEvent("tenant.api_key_rotated"), EventTenantAPIKeyRotated)
}

func TestRotateAPIKeyInput_TenantIDNotMarshaled(t *testing.T) {
	// TenantID doit etre `json:"-"` pour eviter d'etre lu depuis le body
	// (le handler l'injecte depuis le path param).
	input := RotateAPIKeyInput{TenantID: "ws-1", Reason: "test"}
	b, err := json.Marshal(input)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "ws-1", "TenantID ne doit pas etre serialise dans le body")
	assert.Contains(t, string(b), "test")
}

func TestRotateAPIKeyResponse_JSONShape(t *testing.T) {
	// Le contrat Hub attend exactement ces 4 champs.
	resp := RotateAPIKeyResponse{
		TenantID:           "ws-1",
		NewAPIKey:          "jwt.token.here",
		NewAPIKeyEmail:     "veridian-api-ws-1@notifuse.app.veridian.site",
		OldAPIKeyRevokesAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "ws-1", m["tenant_id"])
	assert.Equal(t, "jwt.token.here", m["new_api_key"])
	assert.Equal(t, "veridian-api-ws-1@notifuse.app.veridian.site", m["new_api_key_email"])
	assert.Contains(t, m["old_api_key_revokes_at"], "2026-05-21")
}

func TestTransferOwnerInput_TenantIDNotMarshaled(t *testing.T) {
	input := TransferOwnerInput{TenantID: "ws-1", NewOwnerEmail: "new@x.test", Reason: "test"}
	b, err := json.Marshal(input)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"ws-1"`)
	assert.Contains(t, string(b), "new@x.test")
}

func TestTransferOwnerResponse_JSONShape(t *testing.T) {
	resp := TransferOwnerResponse{
		TenantID:      "ws-1",
		OldOwner:      "old@x.test",
		NewOwner:      "new@x.test",
		TransferredAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "ws-1", m["tenant_id"])
	assert.Equal(t, "old@x.test", m["old_owner"])
	assert.Equal(t, "new@x.test", m["new_owner"])
	assert.Contains(t, m["transferred_at"], "2026-05-21")
}

func TestAPIKeyGraceEntry_JSONShape(t *testing.T) {
	entry := APIKeyGraceEntry{
		APIKeyUserID: "user-uuid",
		WorkspaceID:  "ws-1",
		RevokeAt:     time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC),
		Reason:       "scheduled rotation",
		CreatedAt:    time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "user-uuid", m["api_key_user_id"])
	assert.Equal(t, "ws-1", m["workspace_id"])
	assert.Equal(t, "scheduled rotation", m["reason"])
}

func TestAPIKeyGraceEntry_ReasonOmittedWhenEmpty(t *testing.T) {
	// reason est optionnel et omitempty — verifier qu'il disparait du JSON
	// quand vide.
	entry := APIKeyGraceEntry{
		APIKeyUserID: "user-uuid",
		WorkspaceID:  "ws-1",
		RevokeAt:     time.Now(),
		CreatedAt:    time.Now(),
	}
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"reason"`)
}
