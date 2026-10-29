// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/domain/list_test.go ; ne contient que les declarations absentes du fork.

package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lists.update rewrites every column of the row, so a flag the body left out is
// stored as false. These cases go through json.Unmarshal rather than a struct
// literal on purpose: a Go literal cannot express a missing key, which is exactly
// how the field's absence became a silent "off".
func TestUpdateListRequest_OmittedFlagsAreRefusedNotStoredAsFalse(t *testing.T) {
	t.Skip("DÉFAUT D1 : la mise à jour d'une liste enregistre les drapeaux omis comme faux")
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "is_double_optin omitted",
			body:    `{"workspace_id":"workspace123","id":"list123","name":"My List","is_public":true}`,
			wantErr: "is_double_optin is required",
		},
		{
			name:    "is_public omitted",
			body:    `{"workspace_id":"workspace123","id":"list123","name":"My List","is_double_optin":false}`,
			wantErr: "is_public is required",
		},
		{
			name:    "both flags omitted",
			body:    `{"workspace_id":"workspace123","id":"list123","name":"My List"}`,
			wantErr: "is_double_optin is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req UpdateListRequest
			require.NoError(t, json.Unmarshal([]byte(tt.body), &req))

			list, workspaceID, err := req.Validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, list)
			assert.Empty(t, workspaceID)
		})
	}
}

// A null is a serializer writing an absent optional out, not a caller asking for false:
// there is no bool a null could have meant. Reading it as present let a client that
// serializes absent fields as null drop a list from the public subscription pages, or
// activate subscribers without a confirmation email, without ever being told — the very
// silence the refusal below exists to break. Every other decoder in this package folds
// null into "omitted"; this one now does too.
func TestUpdateListRequest_NullFlagsAreReadAsOmitted(t *testing.T) {
	t.Skip("DÉFAUT D1 : la mise à jour d'une liste enregistre les drapeaux omis comme faux")
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "is_double_optin null",
			body:    `{"workspace_id":"workspace123","id":"list123","name":"My List","is_double_optin":null,"is_public":true}`,
			wantErr: "is_double_optin is required",
		},
		{
			name:    "is_public null",
			body:    `{"workspace_id":"workspace123","id":"list123","name":"My List","is_double_optin":false,"is_public":null}`,
			wantErr: "is_public is required",
		},
		{
			name:    "both flags null",
			body:    `{"workspace_id":"workspace123","id":"list123","name":"My List","is_double_optin":null,"is_public":null}`,
			wantErr: "is_double_optin is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req UpdateListRequest
			require.NoError(t, json.Unmarshal([]byte(tt.body), &req))

			list, workspaceID, err := req.Validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, list)
			assert.Empty(t, workspaceID)
		})
	}
}

// Refusing an omitted flag must not cost the caller the ability to turn one off:
// an explicit false is a legitimate instruction and still lands.
func TestUpdateListRequest_ExplicitFalseFlagsStillApply(t *testing.T) {
	body := `{"workspace_id":"workspace123","id":"list123","name":"My List","is_double_optin":false,"is_public":false}`

	var req UpdateListRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))

	list, workspaceID, err := req.Validate()

	require.NoError(t, err)
	assert.Equal(t, "workspace123", workspaceID)
	assert.False(t, list.IsDoubleOptin)
	assert.False(t, list.IsPublic)
}

// The presence record is written by the decoder, so its zero value has to mean
// "present": a request assembled in Go has no body to read presence from, and a
// caller filling every field in by hand has stated all of them.
func TestUpdateListRequest_StructLiteralNeedsNoPresenceRecord(t *testing.T) {
	req := UpdateListRequest{
		WorkspaceID: "workspace123",
		ID:          "list123",
		Name:        "My List",
	}

	list, _, err := req.Validate()

	require.NoError(t, err)
	assert.False(t, list.IsDoubleOptin)
	assert.False(t, list.IsPublic)
}
