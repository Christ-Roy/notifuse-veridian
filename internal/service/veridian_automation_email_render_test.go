package service

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderAutomationEmail_RendersTemplateForContact(t *testing.T) {
	out, err := renderAutomationEmail(automationEmailRenderInput{
		Template:       createTestTemplate(),
		Workspace:      createTestWorkspaceWithEmailProvider(),
		APIEndpoint:    "https://api.example.com",
		AutomationID:   "auto1",
		AutomationName: "Test Automation",
		ListID:         "list1",
		ListName:       "Test List",
		TemplateID:     "tpl123",
		MessageID:      "ws1_msg1",
		Contact:        &domain.Contact{Email: "recipient@example.com"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Test Subject", out.Subject)
	assert.Equal(t, "Bonjour recipient@example.com", out.Text)
	assert.NotEmpty(t, out.HTML)
	assert.True(t, out.PlainTextOnly)
	assert.Equal(t, "sender1", out.SenderID)
}

func TestRenderAutomationEmail_RefusesIncompleteInputAndMissingEmailContent(t *testing.T) {
	_, err := renderAutomationEmail(automationEmailRenderInput{})
	require.Error(t, err)

	tpl := createTestTemplate()
	tpl.Email = nil
	_, err = renderAutomationEmail(automationEmailRenderInput{
		Template: tpl, Workspace: createTestWorkspaceWithEmailProvider(), TemplateID: "tpl123", MessageID: "ws1_msg1",
		Contact: &domain.Contact{Email: "recipient@example.com"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no email content")
}
