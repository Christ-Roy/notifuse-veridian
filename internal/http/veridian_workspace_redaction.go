package http

import "github.com/Notifuse/notifuse/internal/domain"

// veridianRedactWorkspaceForAPI returns a response clone with SMTP credentials
// reduced to non-sensitive configured flags. The stored/decrypted workspace is
// never mutated because workers still need its credentials.
func veridianRedactWorkspaceForAPI(workspace *domain.Workspace) *domain.Workspace {
	if workspace == nil {
		return nil
	}
	clone := *workspace
	if len(workspace.Integrations) > 0 {
		clone.Integrations = make(domain.Integrations, len(workspace.Integrations))
		copy(clone.Integrations, workspace.Integrations)
	} else {
		clone.Integrations = nil
	}
	for i := range clone.Integrations {
		provider := clone.Integrations[i].EmailProvider
		if clone.Integrations[i].Type != domain.IntegrationTypeEmail {
			continue
		}
		if provider.SMTP != nil {
			smtp := *provider.SMTP
			smtp.HasPassword = smtp.Password != "" || smtp.EncryptedPassword != ""
			smtp.HasOAuth2ClientSecret = smtp.OAuth2ClientSecret != "" || smtp.EncryptedOAuth2ClientSecret != ""
			smtp.HasOAuth2RefreshToken = smtp.OAuth2RefreshToken != "" || smtp.EncryptedOAuth2RefreshToken != ""
			provider.VeridianCredentialsConfigured = smtp.HasPassword ||
				(smtp.HasOAuth2ClientSecret && (smtp.OAuth2Provider != "google" || smtp.HasOAuth2RefreshToken))
			smtp.Password, smtp.EncryptedUsername, smtp.EncryptedPassword = "", "", ""
			smtp.OAuth2ClientSecret, smtp.OAuth2RefreshToken = "", ""
			smtp.EncryptedOAuth2ClientSecret, smtp.EncryptedOAuth2RefreshToken = "", ""
			provider.SMTP = &smtp
		}
		if provider.SES != nil {
			settings := *provider.SES
			provider.VeridianCredentialsConfigured = settings.SecretKey != "" || settings.EncryptedSecretKey != ""
			settings.SecretKey, settings.EncryptedSecretKey = "", ""
			provider.SES = &settings
		}
		if provider.SparkPost != nil {
			settings := *provider.SparkPost
			provider.VeridianCredentialsConfigured = settings.APIKey != "" || settings.EncryptedAPIKey != ""
			settings.APIKey, settings.EncryptedAPIKey = "", ""
			provider.SparkPost = &settings
		}
		if provider.Postmark != nil {
			settings := *provider.Postmark
			provider.VeridianCredentialsConfigured = settings.ServerToken != "" || settings.EncryptedServerToken != ""
			settings.ServerToken, settings.EncryptedServerToken = "", ""
			provider.Postmark = &settings
		}
		if provider.Mailgun != nil {
			settings := *provider.Mailgun
			provider.VeridianCredentialsConfigured = settings.APIKey != "" || settings.EncryptedAPIKey != ""
			settings.APIKey, settings.EncryptedAPIKey = "", ""
			provider.Mailgun = &settings
		}
		if provider.Mailjet != nil {
			settings := *provider.Mailjet
			provider.VeridianCredentialsConfigured = settings.APIKey != "" || settings.EncryptedAPIKey != "" || settings.SecretKey != "" || settings.EncryptedSecretKey != ""
			settings.APIKey, settings.EncryptedAPIKey, settings.SecretKey, settings.EncryptedSecretKey = "", "", "", ""
			provider.Mailjet = &settings
		}
		if provider.SendGrid != nil {
			settings := *provider.SendGrid
			provider.VeridianCredentialsConfigured = settings.APIKey != "" || settings.EncryptedAPIKey != ""
			settings.APIKey, settings.EncryptedAPIKey = "", ""
			provider.SendGrid = &settings
		}
		clone.Integrations[i].EmailProvider = provider
	}
	return &clone
}

func veridianRedactWorkspacesForAPI(workspaces []*domain.Workspace) []*domain.Workspace {
	result := make([]*domain.Workspace, len(workspaces))
	for i, workspace := range workspaces {
		result[i] = veridianRedactWorkspaceForAPI(workspace)
	}
	return result
}
