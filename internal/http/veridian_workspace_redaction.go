package http

import "github.com/Notifuse/notifuse/internal/domain"

// veridianRedactWorkspaceForAPI returns a response clone with every workspace
// credential removed. The stored/decrypted workspace is never mutated because
// workers still need those credentials after a handler writes its response.
func veridianRedactWorkspaceForAPI(workspace *domain.Workspace) *domain.Workspace {
	if workspace == nil {
		return nil
	}
	clone := *workspace
	clone.Settings.EncryptedSecretKey = ""
	clone.Settings.FileManager.HasSecretKey =
		workspace.Settings.FileManager.SecretKey != "" || workspace.Settings.FileManager.EncryptedSecretKey != ""
	clone.Settings.FileManager.SecretKey = ""
	clone.Settings.FileManager.EncryptedSecretKey = ""

	if len(workspace.Integrations) > 0 {
		clone.Integrations = make(domain.Integrations, len(workspace.Integrations))
		copy(clone.Integrations, workspace.Integrations)
	} else {
		clone.Integrations = nil
	}
	for i := range clone.Integrations {
		integration := &clone.Integrations[i]
		// Redact every populated credential-bearing branch, even if a malformed
		// legacy record carries settings inconsistent with Integration.Type.
		redactEmailProvider(integration)
		if integration.SupabaseSettings != nil {
			settings := *integration.SupabaseSettings
			settings.AuthEmailHook.HasSignatureKey =
				settings.AuthEmailHook.SignatureKey != "" || settings.AuthEmailHook.EncryptedSignatureKey != ""
			settings.AuthEmailHook.SignatureKey = ""
			settings.AuthEmailHook.EncryptedSignatureKey = ""
			settings.BeforeUserCreatedHook.HasSignatureKey =
				settings.BeforeUserCreatedHook.SignatureKey != "" || settings.BeforeUserCreatedHook.EncryptedSignatureKey != ""
			settings.BeforeUserCreatedHook.SignatureKey = ""
			settings.BeforeUserCreatedHook.EncryptedSignatureKey = ""
			integration.SupabaseSettings = &settings
		}
		if integration.LLMProvider != nil {
			provider := *integration.LLMProvider
			if provider.Anthropic != nil {
				settings := *provider.Anthropic
				settings.APIKey, settings.EncryptedAPIKey = "", ""
				provider.Anthropic = &settings
			}
			if provider.OpenAI != nil {
				settings := *provider.OpenAI
				settings.APIKey, settings.EncryptedAPIKey = "", ""
				provider.OpenAI = &settings
			}
			integration.LLMProvider = &provider
		}
		if integration.FirecrawlSettings != nil {
			settings := *integration.FirecrawlSettings
			settings.APIKey, settings.EncryptedAPIKey = "", ""
			integration.FirecrawlSettings = &settings
		}
		if integration.IMAPSettings != nil {
			settings := *integration.IMAPSettings
			settings.HasPassword = settings.Password != "" || settings.EncryptedPassword != ""
			settings.Password, settings.EncryptedPassword = "", ""
			integration.IMAPSettings = &settings
		}
	}
	return &clone
}

func redactEmailProvider(integration *domain.Integration) {
	provider := integration.EmailProvider
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
	integration.EmailProvider = provider
}

func veridianRedactWorkspacesForAPI(workspaces []*domain.Workspace) []*domain.Workspace {
	result := make([]*domain.Workspace, len(workspaces))
	for i, workspace := range workspaces {
		result[i] = veridianRedactWorkspaceForAPI(workspace)
	}
	return result
}
