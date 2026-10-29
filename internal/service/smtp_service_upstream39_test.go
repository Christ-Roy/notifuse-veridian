// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/service/smtp_service_test.go ; ne contient que les declarations absentes du fork.

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// indexOfCmd returns the index of the first command equal to needle, or -1.
func indexOfCmd(commands []string, needle string) int {
	for i, c := range commands {
		if c == needle {
			return i
		}
	}
	return -1
}

func TestSendRawEmail_PlainPreferredWhenBothAdvertised(t *testing.T) {
	// Default server advertises "AUTH PLAIN LOGIN"; PLAIN must win.
	server := newMockSMTPServer(t, true)
	defer server.Close()

	port := server.Port()
	msg := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test\r\n\r\nTest body")

	err := sendRawEmail("127.0.0.1", port, "user", "pass", false, "sender@example.com", []string{"recipient@example.com"}, msg)
	require.NoError(t, err)

	commands := server.GetCommands()
	foundPlain := false
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, "AUTH PLAIN") {
			foundPlain = true
		}
		assert.NotEqual(t, "AUTH LOGIN", cmd, "must prefer PLAIN when both are advertised")
	}
	assert.True(t, foundPlain, "expected AUTH PLAIN to be used when advertised")
}
