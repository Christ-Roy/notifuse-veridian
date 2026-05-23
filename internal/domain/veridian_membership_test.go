package domain

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
// Tests colocalises pour les types du contrat sync/remove/restore-member.
// Couvre :
//   - SyncMemberRole.IsValid() : enum check (member|admin only, refuse owner).
//   - Shape JSON Sync/Remove/Restore Input/Response (audit contrat Hub).

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncMemberRole_IsValid_Domain(t *testing.T) {
	assert.True(t, SyncMemberRoleMember.IsValid(), "member doit etre valide")
	assert.True(t, SyncMemberRoleAdmin.IsValid(), "admin doit etre valide")
	assert.False(t, SyncMemberRole("owner").IsValid(), "owner refuse pour sync-member (utiliser provision/transfer-owner)")
	assert.False(t, SyncMemberRole("").IsValid(), "chaine vide invalide")
	assert.False(t, SyncMemberRole("super-admin").IsValid(), "valeur inconnue invalide")
}

// TestSyncMemberInput_JSONShape verifie le contrat JSON cote Hub :
//
//	{"user_email", "hub_user_id", "role", "invited_at", "joined_at"}
//
// Si on change un tag JSON, le Hub reject avec invalid_payload.
func TestSyncMemberInput_JSONShape(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	in := SyncMemberInput{
		UserEmail: "alice@example.com",
		HubUserID: "hub-uuid-1",
		Role:      SyncMemberRoleMember,
		InvitedAt: now,
		JoinedAt:  now,
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, "alice@example.com", got["user_email"])
	assert.Equal(t, "hub-uuid-1", got["hub_user_id"])
	assert.Equal(t, "member", got["role"])
	// TenantID est `json:"-"` (inject path param, pas body) — doit etre absent.
	_, hasTenantID := got["tenant_id"]
	assert.False(t, hasTenantID, "TenantID doit etre `json:\"-\"` (path param, pas body)")
}

// TestSyncMemberResponse_JSONShape : audit contrat reponse 200.
func TestSyncMemberResponse_JSONShape(t *testing.T) {
	out := SyncMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		Synced:    true,
		AppUserID: "user-uuid-notifuse",
		AppRole:   "member",
	}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"tenant_id":"ws-1",
		"user_email":"alice@example.com",
		"synced":true,
		"app_user_id":"user-uuid-notifuse",
		"app_role":"member"
	}`, string(raw))
}

// TestRemoveMemberInput_JSONShape : audit contrat body.
func TestRemoveMemberInput_JSONShape(t *testing.T) {
	in := RemoveMemberInput{
		UserEmail: "alice@example.com",
		Reason:    "admin_action",
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, "alice@example.com", got["user_email"])
	assert.Equal(t, "admin_action", got["reason"])
	_, hasTenantID := got["tenant_id"]
	assert.False(t, hasTenantID)
}

// TestRemoveMemberResponse_JSONShape : audit contrat reponse 200.
func TestRemoveMemberResponse_JSONShape(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	out := RemoveMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "alice@example.com",
		RemovedAt: now,
	}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"tenant_id":"ws-1",
		"user_email":"alice@example.com",
		"removed_at":"2026-05-19T12:00:00Z"
	}`, string(raw))
}

// TestRestoreMemberInput_JSONShape : audit contrat body.
func TestRestoreMemberInput_JSONShape(t *testing.T) {
	in := RestoreMemberInput{
		UserEmail: "alice@example.com",
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"user_email":"alice@example.com"}`, string(raw),
		"RestoreMemberInput minimal — TenantID via path param, pas body")
}

// TestRestoreMemberResponse_JSONShape : audit contrat reponse 200.
func TestRestoreMemberResponse_JSONShape(t *testing.T) {
	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	out := RestoreMemberResponse{
		TenantID:   "ws-1",
		UserEmail:  "alice@example.com",
		RestoredAt: now,
	}
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"tenant_id":"ws-1",
		"user_email":"alice@example.com",
		"restored_at":"2026-05-19T12:00:00Z"
	}`, string(raw))
}

// TestMembership_Types_FieldAuditRegression : garde-fou compile-time pour les
// types publiques exposes. Si on retire un champ, ce test casse au build.
func TestMembership_Types_FieldAuditRegression(t *testing.T) {
	// SyncMemberInput
	syncIn := SyncMemberInput{
		UserEmail: "a", HubUserID: "b", Role: SyncMemberRoleMember,
		InvitedAt: time.Now(), JoinedAt: time.Now(), TenantID: "ws-1",
	}
	assert.Equal(t, "ws-1", syncIn.TenantID)

	// RemoveMemberInput
	rmIn := RemoveMemberInput{UserEmail: "a", Reason: "r", TenantID: "ws-1"}
	assert.Equal(t, "r", rmIn.Reason)

	// RestoreMemberInput
	restIn := RestoreMemberInput{UserEmail: "a", TenantID: "ws-1"}
	assert.Equal(t, "ws-1", restIn.TenantID)

	// Responses
	syncOut := SyncMemberResponse{Synced: true}
	assert.True(t, syncOut.Synced)
	rmOut := RemoveMemberResponse{RemovedAt: time.Now()}
	assert.False(t, rmOut.RemovedAt.IsZero())
	restOut := RestoreMemberResponse{RestoredAt: time.Now()}
	assert.False(t, restOut.RestoredAt.IsZero())
}
