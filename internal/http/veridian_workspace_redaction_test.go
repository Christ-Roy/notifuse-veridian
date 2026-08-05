package http

import (
	"encoding/json"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVeridianRedactWorkspaceForAPIWriteOnlySMTPSecrets(t *testing.T) {
	workspace := &domain.Workspace{Integrations: []domain.Integration{{
		ID: "gmail-1", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{
			Host: "smtp.gmail.com", Username: "owner@gmail.com", Password: "plain-password",
			EncryptedUsername: "encrypted-username", EncryptedPassword: "encrypted-password",
			OAuth2Provider: "google", OAuth2ClientSecret: "plain-client", OAuth2RefreshToken: "plain-refresh",
			EncryptedOAuth2ClientSecret: "encrypted-client", EncryptedOAuth2RefreshToken: "encrypted-refresh",
		}},
	}, {
		ID: "ses", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSES, SES: &domain.AmazonSESSettings{SecretKey: "ses-plain", EncryptedSecretKey: "ses-encrypted"}},
	}, {
		ID: "sparkpost", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSparkPost, SparkPost: &domain.SparkPostSettings{APIKey: "spark-plain", EncryptedAPIKey: "spark-encrypted"}},
	}, {
		ID: "postmark", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindPostmark, Postmark: &domain.PostmarkSettings{ServerToken: "postmark-plain", EncryptedServerToken: "postmark-encrypted"}},
	}, {
		ID: "mailgun", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindMailgun, Mailgun: &domain.MailgunSettings{APIKey: "mailgun-plain", EncryptedAPIKey: "mailgun-encrypted"}},
	}, {
		ID: "mailjet", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindMailjet, Mailjet: &domain.MailjetSettings{APIKey: "mailjet-plain", EncryptedAPIKey: "mailjet-encrypted", SecretKey: "mailjet-secret", EncryptedSecretKey: "mailjet-secret-encrypted"}},
	}, {
		ID: "sendgrid", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSendGrid, SendGrid: &domain.SendGridSettings{APIKey: "sendgrid-plain", EncryptedAPIKey: "sendgrid-encrypted"}},
	}}}

	redacted := veridianRedactWorkspaceForAPI(workspace)
	require.NotSame(t, workspace, redacted)
	require.NotNil(t, redacted.Integrations[0].EmailProvider.SMTP)
	raw, err := json.Marshal(redacted)
	require.NoError(t, err)
	jsonText := string(raw)
	for _, forbidden := range []string{
		"plain-password", "plain-client", "plain-refresh", "encrypted-username", "encrypted-password", "encrypted-client", "encrypted-refresh",
		"encrypted_username", "encrypted_password", "encrypted_oauth2_client_secret", "encrypted_oauth2_refresh_token",
		`"oauth2_client_secret":`, `"oauth2_refresh_token":`,
		"ses-plain", "ses-encrypted", "spark-plain", "spark-encrypted", "postmark-plain", "postmark-encrypted",
		"mailgun-plain", "mailgun-encrypted", "mailjet-plain", "mailjet-encrypted", "mailjet-secret", "mailjet-secret-encrypted",
		"sendgrid-plain", "sendgrid-encrypted", "encrypted_api_key", "encrypted_secret_key", "encrypted_server_token",
	} {
		assert.NotContains(t, jsonText, forbidden)
	}
	assert.Contains(t, jsonText, `"has_password":true`)
	assert.Contains(t, jsonText, `"has_oauth2_client_secret":true`)
	assert.Contains(t, jsonText, `"has_oauth2_refresh_token":true`)
	assert.Contains(t, jsonText, `"veridian_credentials_configured":true`)
	assert.Equal(t, "encrypted-password", workspace.Integrations[0].EmailProvider.SMTP.EncryptedPassword, "redaction must not mutate runtime state")
}
