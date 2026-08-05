package http

import (
	"encoding/json"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianRedactWorkspaceForAPI_AllSecretsWithoutRuntimeMutation(t *testing.T) {
	workspace := &domain.Workspace{
		ID: "workspace-1",
		Settings: domain.WorkspaceSettings{
			WebsiteURL:         "https://example.com",
			SecretKey:          "workspace-runtime-secret",
			EncryptedSecretKey: "workspace-ciphertext",
			FileManager: domain.FileManagerSettings{
				Provider: "s3", Endpoint: "https://s3.example.com", Bucket: "bucket", AccessKey: "visible-access-id",
				SecretKey: "file-runtime-secret", EncryptedSecretKey: "file-ciphertext",
			},
		},
		Integrations: []domain.Integration{
			{
				ID: "email", Type: domain.IntegrationTypeEmail,
				EmailProvider: domain.EmailProvider{
					Kind: domain.EmailProviderKindSMTP,
					SMTP: &domain.SMTPSettings{
						Host: "smtp.example.com", Username: "visible@example.com",
						Password: "smtp-password", EncryptedUsername: "smtp-user-ciphertext", EncryptedPassword: "smtp-password-ciphertext",
						OAuth2ClientSecret: "oauth-client-secret", OAuth2RefreshToken: "oauth-refresh-secret",
						EncryptedOAuth2ClientSecret: "oauth-client-ciphertext", EncryptedOAuth2RefreshToken: "oauth-refresh-ciphertext",
					},
					SES:       &domain.AmazonSESSettings{AccessKey: "visible-ses-access-id", SecretKey: "ses-secret", EncryptedSecretKey: "ses-ciphertext"},
					SparkPost: &domain.SparkPostSettings{APIKey: "spark-secret", EncryptedAPIKey: "spark-ciphertext"},
					Postmark:  &domain.PostmarkSettings{ServerToken: "postmark-secret", EncryptedServerToken: "postmark-ciphertext"},
					Mailgun:   &domain.MailgunSettings{APIKey: "mailgun-secret", EncryptedAPIKey: "mailgun-ciphertext", Domain: "mail.example.com"},
					Mailjet:   &domain.MailjetSettings{APIKey: "mailjet-api-secret", EncryptedAPIKey: "mailjet-api-ciphertext", SecretKey: "mailjet-secret", EncryptedSecretKey: "mailjet-secret-ciphertext"},
					SendGrid:  &domain.SendGridSettings{APIKey: "sendgrid-secret", EncryptedAPIKey: "sendgrid-ciphertext"},
				},
			},
			{
				ID: "supabase", Type: domain.IntegrationTypeSupabase,
				SupabaseSettings: &domain.SupabaseIntegrationSettings{
					AuthEmailHook: domain.SupabaseAuthEmailHookSettings{SignatureKey: "supabase-auth-secret", EncryptedSignatureKey: "supabase-auth-ciphertext"},
					BeforeUserCreatedHook: domain.SupabaseUserCreatedHookSettings{
						SignatureKey: "supabase-user-secret", EncryptedSignatureKey: "supabase-user-ciphertext",
						AddUserToLists: []string{"list-1"}, CustomJSONField: "custom_json_1", RejectDisposableEmail: true,
					},
				},
			},
			{
				ID: "llm", Type: domain.IntegrationTypeLLM,
				LLMProvider: &domain.LLMProvider{
					Kind:      domain.LLMProviderKindAnthropic,
					Anthropic: &domain.AnthropicSettings{Model: "claude-test", APIKey: "anthropic-secret", EncryptedAPIKey: "anthropic-ciphertext"},
					OpenAI:    &domain.OpenAISettings{Model: "gpt-test", BaseURL: "https://llm.example.com", APIKey: "openai-secret", EncryptedAPIKey: "openai-ciphertext"},
				},
			},
			{ID: "firecrawl", Type: domain.IntegrationTypeFirecrawl, FirecrawlSettings: &domain.FirecrawlSettings{BaseURL: "https://firecrawl.example.com", APIKey: "firecrawl-secret", EncryptedAPIKey: "firecrawl-ciphertext"}},
			{ID: "imap", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "imap.example.com", Port: 993, Username: "visible-imap-user", Password: "imap-secret", EncryptedPassword: "imap-ciphertext", UseTLS: true}},
		},
	}

	redacted := veridianRedactWorkspaceForAPI(workspace)
	require.NotSame(t, workspace, redacted)
	require.Len(t, redacted.Integrations, len(workspace.Integrations))

	raw, err := json.Marshal(redacted)
	require.NoError(t, err)
	jsonText := string(raw)
	for _, forbiddenField := range []string{
		`"encrypted_secret_key":`, `"secret_key":`, `"encrypted_username":`,
		`"encrypted_password":`, `"password":`, `"oauth2_client_secret":`,
		`"oauth2_refresh_token":`, `"encrypted_oauth2_client_secret":`,
		`"encrypted_oauth2_refresh_token":`, `"api_key":`, `"encrypted_api_key":`,
		`"server_token":`, `"encrypted_server_token":`, `"signature_key":`,
		`"encrypted_signature_key":`,
	} {
		assert.NotContains(t, jsonText, forbiddenField)
	}
	for _, forbiddenValue := range []string{
		"workspace-runtime-secret", "workspace-ciphertext", "file-runtime-secret", "file-ciphertext",
		"smtp-password", "smtp-user-ciphertext", "smtp-password-ciphertext", "oauth-client-secret", "oauth-refresh-secret",
		"oauth-client-ciphertext", "oauth-refresh-ciphertext", "ses-secret", "ses-ciphertext", "spark-secret", "spark-ciphertext",
		"postmark-secret", "postmark-ciphertext", "mailgun-secret", "mailgun-ciphertext", "mailjet-api-secret",
		"mailjet-api-ciphertext", "mailjet-secret", "mailjet-secret-ciphertext", "sendgrid-secret", "sendgrid-ciphertext",
		"supabase-auth-secret", "supabase-auth-ciphertext", "supabase-user-secret", "supabase-user-ciphertext",
		"anthropic-secret", "anthropic-ciphertext", "openai-secret", "openai-ciphertext",
		"firecrawl-secret", "firecrawl-ciphertext", "imap-secret", "imap-ciphertext",
	} {
		assert.NotContains(t, jsonText, forbiddenValue)
	}

	assert.Contains(t, jsonText, `"has_secret_key":true`)
	assert.Contains(t, jsonText, `"has_signature_key":true`)
	assert.Contains(t, jsonText, `"has_password":true`)
	assert.Contains(t, jsonText, `"veridian_credentials_configured":true`)
	assert.Contains(t, jsonText, "visible-access-id")
	assert.Contains(t, jsonText, "visible-imap-user")
	assert.Contains(t, jsonText, "claude-test")
	assert.Contains(t, jsonText, "https://firecrawl.example.com")
	assert.Contains(t, jsonText, "list-1")
	assert.True(t, redacted.Integrations[4].IMAPSettings.HasPassword)

	// Every credential-bearing pointer must have been cloned before redaction.
	assert.NotSame(t, workspace.Integrations[0].EmailProvider.SMTP, redacted.Integrations[0].EmailProvider.SMTP)
	assert.NotSame(t, workspace.Integrations[1].SupabaseSettings, redacted.Integrations[1].SupabaseSettings)
	assert.NotSame(t, workspace.Integrations[2].LLMProvider, redacted.Integrations[2].LLMProvider)
	assert.NotSame(t, workspace.Integrations[2].LLMProvider.Anthropic, redacted.Integrations[2].LLMProvider.Anthropic)
	assert.NotSame(t, workspace.Integrations[3].FirecrawlSettings, redacted.Integrations[3].FirecrawlSettings)
	assert.NotSame(t, workspace.Integrations[4].IMAPSettings, redacted.Integrations[4].IMAPSettings)

	// The original object remains fully hydrated for workers and persistence.
	assert.Equal(t, "workspace-ciphertext", workspace.Settings.EncryptedSecretKey)
	assert.Equal(t, "file-runtime-secret", workspace.Settings.FileManager.SecretKey)
	assert.Equal(t, "file-ciphertext", workspace.Settings.FileManager.EncryptedSecretKey)
	assert.Equal(t, "smtp-password-ciphertext", workspace.Integrations[0].EmailProvider.SMTP.EncryptedPassword)
	assert.Equal(t, "supabase-auth-secret", workspace.Integrations[1].SupabaseSettings.AuthEmailHook.SignatureKey)
	assert.Equal(t, "anthropic-secret", workspace.Integrations[2].LLMProvider.Anthropic.APIKey)
	assert.Equal(t, "firecrawl-secret", workspace.Integrations[3].FirecrawlSettings.APIKey)
	assert.Equal(t, "imap-secret", workspace.Integrations[4].IMAPSettings.Password)
}

func TestVeridianRedactWorkspacesForAPI_DoesNotAliasInputSlice(t *testing.T) {
	original := []*domain.Workspace{{ID: "one"}, nil}
	redacted := veridianRedactWorkspacesForAPI(original)
	require.Len(t, redacted, 2)
	assert.NotSame(t, original[0], redacted[0])
	assert.Nil(t, redacted[1])
	redacted[0].ID = "changed"
	assert.Equal(t, "one", original[0].ID)
}
