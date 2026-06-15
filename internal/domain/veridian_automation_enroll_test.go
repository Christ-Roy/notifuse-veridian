package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianEnrollContactsRequest_Validate(t *testing.T) {
	t.Run("happy path normalizes and dedupes", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{
			WorkspaceID:   "ws1",
			AutomationID:  "auto1",
			ContactEmails: []string{" Alice@Example.com ", "bob@example.com", "ALICE@example.com"},
		}
		emails, err := req.Validate()
		require.NoError(t, err)
		// alice deduped (lowercased+trimmed), order preserved
		assert.Equal(t, []string{"alice@example.com", "bob@example.com"}, emails)
	})

	t.Run("missing workspace_id", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{AutomationID: "a", ContactEmails: []string{"x@y.com"}}
		_, err := req.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "workspace_id")
	})

	t.Run("missing automation_id", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{WorkspaceID: "w", ContactEmails: []string{"x@y.com"}}
		_, err := req.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "automation_id")
	})

	t.Run("empty contact_emails", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{WorkspaceID: "w", AutomationID: "a", ContactEmails: nil}
		_, err := req.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "contact_emails")
	})

	t.Run("empty string email rejected", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{WorkspaceID: "w", AutomationID: "a", ContactEmails: []string{"  "}}
		_, err := req.Validate()
		require.Error(t, err)
	})

	t.Run("invalid email format rejected", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{WorkspaceID: "w", AutomationID: "a", ContactEmails: []string{"not-an-email"}}
		_, err := req.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid email format")
	})

	t.Run("display name / angle brackets rejected (bare address required)", func(t *testing.T) {
		req := &VeridianEnrollContactsRequest{
			WorkspaceID:   "w",
			AutomationID:  "a",
			ContactEmails: []string{"Alice <alice@example.com>"},
		}
		_, err := req.Validate()
		require.Error(t, err)
	})

	t.Run("batch over cap rejected", func(t *testing.T) {
		emails := make([]string, maxEnrollBatch+1)
		for i := range emails {
			emails[i] = "user" + itoaTest(i) + "@example.com"
		}
		req := &VeridianEnrollContactsRequest{WorkspaceID: "w", AutomationID: "a", ContactEmails: emails}
		_, err := req.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many contacts")
	})
}

// itoaTest is a tiny helper to avoid importing strconv in the test for a single use.
func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
