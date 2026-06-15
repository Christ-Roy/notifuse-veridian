package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianMessageIDForSend(t *testing.T) {
	tests := []struct {
		name        string
		messageID   string
		fromAddress string
		want        string
	}{
		{
			name:        "nominal",
			messageID:   "11111111-2222-3333-4444-555555555555",
			fromAddress: "contact@send.veridian.site",
			want:        "11111111-2222-3333-4444-555555555555@send.veridian.site",
		},
		{
			name:        "display name in from",
			messageID:   "abc",
			fromAddress: "Robert <robert@agences-veridian.fr>",
			want:        "abc@agences-veridian.fr",
		},
		{
			name:        "empty message id -> fallback (empty)",
			messageID:   "",
			fromAddress: "x@y.z",
			want:        "",
		},
		{
			name:        "from without host -> fallback (empty)",
			messageID:   "abc",
			fromAddress: "not-an-email",
			want:        "",
		},
		{
			name:        "from with trailing @ -> fallback (empty)",
			messageID:   "abc",
			fromAddress: "user@",
			want:        "",
		},
		{
			name:        "whitespace trimmed",
			messageID:   "  id-1  ",
			fromAddress: "  a@b.io ",
			want:        "id-1@b.io",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, veridianMessageIDForSend(tt.messageID, tt.fromAddress))
		})
	}
}

func TestVeridianHostFromEmail(t *testing.T) {
	assert.Equal(t, "host.io", veridianHostFromEmail("user@host.io"))
	assert.Equal(t, "acme.fr", veridianHostFromEmail("Jane <jane@acme.fr>"))
	assert.Equal(t, "", veridianHostFromEmail("nope"))
	assert.Equal(t, "", veridianHostFromEmail("user@"))
	assert.Equal(t, "", veridianHostFromEmail(""))
}
