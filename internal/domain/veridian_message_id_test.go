package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianBareMessageUUID(t *testing.T) {
	tests := []struct {
		name   string
		fullID string
		want   string
	}{
		{
			name:   "nominal workspace-prefixed id",
			fullID: "coldtunnel_fc75fbbd-05d3-4938-b722-59388edcf803",
			want:   "fc75fbbd-05d3-4938-b722-59388edcf803",
		},
		{
			name:   "workspace id itself contains underscores",
			fullID: "acme_prod_v2_fc75fbbd-05d3-4938-b722-59388edcf803",
			want:   "fc75fbbd-05d3-4938-b722-59388edcf803",
		},
		{
			name:   "already bare UUID, no prefix",
			fullID: "fc75fbbd-05d3-4938-b722-59388edcf803",
			want:   "fc75fbbd-05d3-4938-b722-59388edcf803",
		},
		{
			name:   "too short to contain a UUID -> unchanged",
			fullID: "abc",
			want:   "abc",
		},
		{
			name:   "empty -> unchanged",
			fullID: "",
			want:   "",
		},
		{
			name:   "36 trailing chars but not a well-formed UUID -> unchanged",
			fullID: "coldtunnel_not-a-real-uuid-at-all-xxxxxxxxxx",
			want:   "coldtunnel_not-a-real-uuid-at-all-xxxxxxxxxx",
		},
		{
			name:   "uppercase UUID suffix does not match (canonical form is lowercase) -> unchanged",
			fullID: "coldtunnel_FC75FBBD-05D3-4938-B722-59388EDCF803",
			want:   "coldtunnel_FC75FBBD-05D3-4938-B722-59388EDCF803",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianBareMessageUUID(tt.fullID))
		})
	}
}

func TestVeridianReconstructStoredMessageID(t *testing.T) {
	const workspaceID = "coldtunnel"
	const bareUUID = "fc75fbbd-05d3-4938-b722-59388edcf803"

	tests := []struct {
		name        string
		workspaceID string
		messageID   string
		want        string
	}{
		{
			name:        "bare UUID reconstructed with workspace prefix",
			workspaceID: workspaceID,
			messageID:   bareUUID,
			want:        workspaceID + "_" + bareUUID,
		},
		{
			name:        "already prefixed by this workspace -> unchanged (legacy rows)",
			workspaceID: workspaceID,
			messageID:   workspaceID + "_" + bareUUID,
			want:        workspaceID + "_" + bareUUID,
		},
		{
			name:        "not a UUID at all -> unchanged, lookup will simply miss",
			workspaceID: workspaceID,
			messageID:   "some-other-thread-id@gmail.com",
			want:        "some-other-thread-id@gmail.com",
		},
		{
			name:        "empty message id -> unchanged",
			workspaceID: workspaceID,
			messageID:   "",
			want:        "",
		},
		{
			name:        "empty workspace id -> unchanged",
			workspaceID: "",
			messageID:   bareUUID,
			want:        bareUUID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianReconstructStoredMessageID(tt.workspaceID, tt.messageID))
		})
	}
}

// TestVeridianMessageIDRoundTrip prouve le contrat bout-en-bout : ce qu'on strippe à
// l'envoi (VeridianBareMessageUUID) se reconstruit exactement à la réception
// (VeridianReconstructStoredMessageID), pour n'importe quel workspace_id, y compris
// un qui contient lui-même des underscores.
func TestVeridianMessageIDRoundTrip(t *testing.T) {
	cases := []string{"coldtunnel", "acme_prod_v2", "a", ""}
	uuid := "fc75fbbd-05d3-4938-b722-59388edcf803"
	for _, ws := range cases {
		t.Run("workspace="+ws, func(t *testing.T) {
			if ws == "" {
				t.Skip("empty workspace id is not a real case (no workspace has an empty id)")
			}
			stored := ws + "_" + uuid
			bare := VeridianBareMessageUUID(stored)
			assert.Equal(t, uuid, bare, "stripping must yield the bare uuid")
			reconstructed := VeridianReconstructStoredMessageID(ws, bare)
			assert.Equal(t, stored, reconstructed, "reconstruction must yield back the exact stored id")
		})
	}
}
