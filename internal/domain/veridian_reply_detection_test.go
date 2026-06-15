package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianExtractMessageIDLocalParts(t *testing.T) {
	tests := []struct {
		name       string
		inReplyTo  string
		references []string
		want       []string
	}{
		{
			name:      "empty",
			inReplyTo: "",
			want:      []string{},
		},
		{
			name:      "in-reply-to with chevrons and domain",
			inReplyTo: "<abc-123@send.veridian.site>",
			want:      []string{"abc-123"},
		},
		{
			name:      "in-reply-to without chevrons",
			inReplyTo: "abc-123@send.veridian.site",
			want:      []string{"abc-123"},
		},
		{
			name:      "local-part only (no @)",
			inReplyTo: "<just-an-id>",
			want:      []string{"just-an-id"},
		},
		{
			name:       "references chain, ordered, dedup with in-reply-to",
			inReplyTo:  "<msg-2@host>",
			references: []string{"<msg-0@host> <msg-1@host>", "<msg-2@host>"},
			// in-reply-to first (msg-2), then references (msg-0, msg-1), msg-2 deduped.
			want: []string{"msg-2", "msg-0", "msg-1"},
		},
		{
			name:       "references only",
			references: []string{"<root@a>", "<reply@b>"},
			want:       []string{"root", "reply"},
		},
		{
			name:      "whitespace tolerated",
			inReplyTo: "   <  trim-me@host >   ",
			want:      []string{"trim-me"},
		},
		{
			name:      "multiple ids in one header field",
			inReplyTo: "<id-a@h> <id-b@h>",
			want:      []string{"id-a", "id-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VeridianExtractMessageIDLocalParts(tt.inReplyTo, tt.references)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVeridianNormalizeEmail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Prospect@Example.COM", "prospect@example.com"},
		{"display name", "Jane Doe <Jane.Doe@Acme.fr>", "jane.doe@acme.fr"},
		{"whitespace", "   bob@host.io  ", "bob@host.io"},
		{"empty", "", ""},
		{"angle only", "<a@b>", "a@b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianNormalizeEmail(tt.in))
		})
	}
}

func TestVeridianFromLooksLikeDaemon(t *testing.T) {
	tests := []struct {
		name string
		from string
		want bool
	}{
		{"mailer-daemon", "MAILER-DAEMON@send.veridian.site", true},
		{"postmaster", "postmaster@acme.fr", true},
		{"mail delivery system display", "Mail Delivery System <mailer-daemon@x.fr>", true},
		{"mdaemon", "MDaemon@host", true},
		{"empty (no addressable sender)", "", true},
		{"real human prospect", "prospect@acme.fr", false},
		{"human display name", "Jane Doe <jane@acme.fr>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianFromLooksLikeDaemon(tt.from))
		})
	}
}

func TestVeridianReplyMatchTypeConstants(t *testing.T) {
	// Garde-fou : les valeurs sérialisées (timeline, audit) ne doivent pas dériver.
	assert.Equal(t, VeridianReplyMatchType(""), VeridianReplyMatchNone)
	assert.Equal(t, VeridianReplyMatchType("message_id"), VeridianReplyMatchMessageID)
	assert.Equal(t, VeridianReplyMatchType("sender_fallback"), VeridianReplyMatchSenderFallback)
}
