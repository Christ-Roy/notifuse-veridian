package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gmailCreateRequest() VeridianCreateEmailProfileRequest {
	return VeridianCreateEmailProfileRequest{
		WorkspaceID: "ws1", Type: VeridianCreateProfileTypeGmailAppPassword,
		Name: "Gmail Robert", SenderEmail: "robert@gmail.com", SenderName: "Robert",
		AppPassword: "abcd efgh ijkl mnop",
	}
}

func TestVeridianCreateEmailProfileRequestValidate(t *testing.T) {
	good := gmailCreateRequest()
	require.NoError(t, good.Validate())

	for name, mutate := range map[string]func(*VeridianCreateEmailProfileRequest){
		"workspace":      func(r *VeridianCreateEmailProfileRequest) { r.WorkspaceID = "" },
		"expediteur":     func(r *VeridianCreateEmailProfileRequest) { r.SenderEmail = "pas-une-adresse" },
		"mot de passe":   func(r *VeridianCreateEmailProfileRequest) { r.AppPassword = " " },
		"type de compte": func(r *VeridianCreateEmailProfileRequest) { r.GmailAccountType = "pro" },
		"type inconnu":   func(r *VeridianCreateEmailProfileRequest) { r.Type = "ses" },
		"smtp manquant":  func(r *VeridianCreateEmailProfileRequest) { r.Type = VeridianCreateProfileTypeSMTPIMAP },
	} {
		t.Run(name, func(t *testing.T) {
			r := gmailCreateRequest()
			mutate(&r)
			require.Error(t, r.Validate())
		})
	}

	smtp := func() VeridianCreateEmailProfileRequest {
		return VeridianCreateEmailProfileRequest{
			WorkspaceID: "ws1", Type: VeridianCreateProfileTypeSMTPIMAP, SenderEmail: "a@b.fr",
			SMTP: &VeridianCreateProfileSMTP{Host: "h", Port: 587, Username: "u", Password: "p"},
		}
	}
	t.Run("smtp sans imap est valide", func(t *testing.T) {
		r := smtp()
		require.NoError(t, r.Validate())
	})
	t.Run("port smtp hors plage", func(t *testing.T) {
		r := smtp()
		r.SMTP.Port = 70000
		require.Error(t, r.Validate())
	})
	t.Run("imap incomplet", func(t *testing.T) {
		r := smtp()
		r.IMAP = &VeridianCreateProfileIMAP{Host: "h", Port: 993, Username: "u"}
		require.Error(t, r.Validate())
	})
	t.Run("imap complet", func(t *testing.T) {
		r := smtp()
		r.IMAP = &VeridianCreateProfileIMAP{Host: "h", Port: 993, Username: "u", Password: "p"}
		require.NoError(t, r.Validate())
	})
}

func TestVeridianCreateEmailProfileConstantsAreStable(t *testing.T) {
	// Contrat avec la console (assistant) et le CLI : hôtes, ports et cadence posés à la création.
	assert.Equal(t, "smtp_imap", VeridianCreateProfileTypeSMTPIMAP)
	assert.Equal(t, "gmail_app_password", VeridianCreateProfileTypeGmailAppPassword)
	assert.Equal(t, "smtp.gmail.com", VeridianGmailSMTPHost)
	assert.Equal(t, 587, VeridianGmailSMTPPort)
	assert.Equal(t, "imap.gmail.com", VeridianGmailIMAPHost)
	assert.Equal(t, 993, VeridianGmailIMAPPort)
	assert.Equal(t, 1, VeridianCreateProfileGmailRatePerMinute)
	assert.Equal(t, 60, VeridianCreateProfileDefaultRatePerMinute)
}
