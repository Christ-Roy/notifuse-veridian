package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === FreezeReason.IsValid ====================================================

func TestFreezeReason_IsValid(t *testing.T) {
	cases := []struct {
		reason FreezeReason
		want   bool
	}{
		{FreezeReasonQuotaSeatExceeded, true},
		{FreezeReasonManual, true},
		{"", false},
		{"unknown_reason", false},
		{"QUOTA_SEAT_EXCEEDED", false}, // case-sensitive
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.reason.IsValid(), "FreezeReason(%q).IsValid()", c.reason)
	}
}

// === Webhook events ==========================================================

func TestEventTenantMemberFrozen_Constant(t *testing.T) {
	assert.Equal(t, "tenant.member_frozen", string(EventTenantMemberFrozen),
		"webhook event name must match CONTRAT-HUB §7.1 / §5.21 contract")
}

func TestEventTenantMemberUnfrozen_Constant(t *testing.T) {
	assert.Equal(t, "tenant.member_unfrozen", string(EventTenantMemberUnfrozen),
		"webhook event name must match CONTRAT-HUB §7.1 / §5.21 contract")
}

// === Inputs/Outputs JSON shape ==============================================

func TestFreezeMemberInput_JSONShape(t *testing.T) {
	// TenantID est `json:"-"` (inject par handler depuis path param).
	in := FreezeMemberInput{
		UserEmail: "bob@x.test",
		HubUserID: "hub-u-bob",
		Reason:    FreezeReasonQuotaSeatExceeded,
		TenantID:  "ws-1", // shouldn't appear in JSON
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, `"user_email":"bob@x.test"`)
	assert.Contains(t, s, `"hub_user_id":"hub-u-bob"`)
	assert.Contains(t, s, `"reason":"quota_seat_exceeded"`)
	assert.NotContains(t, s, "ws-1", "TenantID must NOT serialize (json:\"-\")")
}

func TestFreezeMemberResponse_JSONShape(t *testing.T) {
	resp := FreezeMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "bob@x.test",
		HubUserID: "hub-u-bob",
		Reason:    FreezeReasonManual,
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))
	assert.Equal(t, "ws-1", raw["tenant_id"])
	assert.Equal(t, "bob@x.test", raw["user_email"])
	assert.Equal(t, "hub-u-bob", raw["hub_user_id"])
	assert.Equal(t, "manual", raw["reason"])
}

func TestUnfreezeMemberInput_TenantIDNotSerialized(t *testing.T) {
	in := UnfreezeMemberInput{
		UserEmail: "bob@x.test",
		HubUserID: "hub-u-bob",
		TenantID:  "ws-1",
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "ws-1")
}

func TestUnfreezeMemberResponse_HasUnfrozenAt(t *testing.T) {
	resp := UnfreezeMemberResponse{
		TenantID:  "ws-1",
		UserEmail: "bob@x.test",
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &raw))
	_, hasField := raw["unfrozen_at"]
	assert.True(t, hasField, "unfrozen_at field must be present")
}

func TestVeridianFrozenMember_HasPKFields(t *testing.T) {
	// Sanity check : la struct exporte les 4 colonnes PK + payload.
	fm := VeridianFrozenMember{
		WorkspaceID: "ws-1",
		UserID:      "u-1",
		Reason:      FreezeReasonQuotaSeatExceeded,
	}
	assert.Equal(t, "ws-1", fm.WorkspaceID)
	assert.Equal(t, "u-1", fm.UserID)
	assert.Equal(t, FreezeReasonQuotaSeatExceeded, fm.Reason)
}
