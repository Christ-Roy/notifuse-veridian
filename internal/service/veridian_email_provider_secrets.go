package service

import (
	"reflect"

	"github.com/Notifuse/notifuse/internal/domain"
)

func veridianEmailProviderHasClientCiphertext(provider *domain.EmailProvider) bool {
	if provider == nil {
		return false
	}
	if provider.SMTP != nil && (provider.SMTP.EncryptedUsername != "" || provider.SMTP.EncryptedPassword != "" ||
		provider.SMTP.EncryptedOAuth2ClientSecret != "" || provider.SMTP.EncryptedOAuth2RefreshToken != "") {
		return true
	}
	return (provider.SES != nil && provider.SES.EncryptedSecretKey != "") ||
		(provider.SparkPost != nil && provider.SparkPost.EncryptedAPIKey != "") ||
		(provider.Postmark != nil && provider.Postmark.EncryptedServerToken != "") ||
		(provider.Mailgun != nil && provider.Mailgun.EncryptedAPIKey != "") ||
		(provider.Mailjet != nil && (provider.Mailjet.EncryptedAPIKey != "" || provider.Mailjet.EncryptedSecretKey != "")) ||
		(provider.SendGrid != nil && provider.SendGrid.EncryptedAPIKey != "")
}

func veridianClearEmailProviderResponseFlags(provider *domain.EmailProvider) {
	if provider == nil {
		return
	}
	provider.VeridianCredentialsConfigured = false
	if provider.SMTP != nil {
		provider.SMTP.HasPassword = false
		provider.SMTP.HasOAuth2ClientSecret = false
		provider.SMTP.HasOAuth2RefreshToken = false
	}
}

func veridianEmailProviderTransportChanged(next, current *domain.EmailProvider) bool {
	if next == nil || current == nil || next.Kind != current.Kind || !reflect.DeepEqual(next.Senders, current.Senders) {
		return true
	}
	if next.Kind != domain.EmailProviderKindSMTP {
		return true
	}
	if next.SMTP == nil || current.SMTP == nil {
		return next.SMTP != current.SMTP
	}
	a, b := next.SMTP, current.SMTP
	if a.Password != "" || a.OAuth2ClientSecret != "" || a.OAuth2RefreshToken != "" {
		return true
	}
	return a.Host != b.Host || a.Port != b.Port || a.Username != b.Username || a.UseTLS != b.UseTLS ||
		a.SkipTLSVerify != b.SkipTLSVerify || a.EHLOHostname != b.EHLOHostname || a.AuthType != b.AuthType ||
		a.OAuth2Provider != b.OAuth2Provider || a.OAuth2TenantID != b.OAuth2TenantID || a.OAuth2ClientID != b.OAuth2ClientID
}

// veridianPreserveEmailProviderSecrets keeps encrypted credentials when an
// integration is edited without re-entering them. Console forms intentionally
// never prefill plaintext secrets, so an empty secret means "leave unchanged".
func veridianPreserveEmailProviderSecrets(next *domain.EmailProvider, current domain.EmailProvider) {
	if next == nil || next.Kind != current.Kind {
		return
	}

	switch next.Kind {
	case domain.EmailProviderKindSMTP:
		if next.SMTP == nil || current.SMTP == nil {
			return
		}
		if next.SMTP.Password == "" && next.SMTP.EncryptedPassword == "" {
			next.SMTP.EncryptedPassword = current.SMTP.EncryptedPassword
		}
		if next.SMTP.OAuth2ClientSecret == "" && next.SMTP.EncryptedOAuth2ClientSecret == "" {
			next.SMTP.EncryptedOAuth2ClientSecret = current.SMTP.EncryptedOAuth2ClientSecret
		}
		if next.SMTP.OAuth2RefreshToken == "" && next.SMTP.EncryptedOAuth2RefreshToken == "" {
			next.SMTP.EncryptedOAuth2RefreshToken = current.SMTP.EncryptedOAuth2RefreshToken
		}
	case domain.EmailProviderKindSES:
		if next.SES != nil && current.SES != nil && next.SES.SecretKey == "" && next.SES.EncryptedSecretKey == "" {
			next.SES.EncryptedSecretKey = current.SES.EncryptedSecretKey
		}
	case domain.EmailProviderKindSparkPost:
		if next.SparkPost != nil && current.SparkPost != nil && next.SparkPost.APIKey == "" && next.SparkPost.EncryptedAPIKey == "" {
			next.SparkPost.EncryptedAPIKey = current.SparkPost.EncryptedAPIKey
		}
	case domain.EmailProviderKindPostmark:
		if next.Postmark != nil && current.Postmark != nil && next.Postmark.ServerToken == "" && next.Postmark.EncryptedServerToken == "" {
			next.Postmark.EncryptedServerToken = current.Postmark.EncryptedServerToken
		}
	case domain.EmailProviderKindMailgun:
		if next.Mailgun != nil && current.Mailgun != nil && next.Mailgun.APIKey == "" && next.Mailgun.EncryptedAPIKey == "" {
			next.Mailgun.EncryptedAPIKey = current.Mailgun.EncryptedAPIKey
		}
	case domain.EmailProviderKindMailjet:
		if next.Mailjet == nil || current.Mailjet == nil {
			return
		}
		if next.Mailjet.APIKey == "" && next.Mailjet.EncryptedAPIKey == "" {
			next.Mailjet.EncryptedAPIKey = current.Mailjet.EncryptedAPIKey
		}
		if next.Mailjet.SecretKey == "" && next.Mailjet.EncryptedSecretKey == "" {
			next.Mailjet.EncryptedSecretKey = current.Mailjet.EncryptedSecretKey
		}
	case domain.EmailProviderKindSendGrid:
		if next.SendGrid != nil && current.SendGrid != nil && next.SendGrid.APIKey == "" && next.SendGrid.EncryptedAPIKey == "" {
			next.SendGrid.EncryptedAPIKey = current.SendGrid.EncryptedAPIKey
		}
	}
}

// veridianHydrateEmailProviderSecrets decrypts credentials carried by a saved
// provider before a connection test. Plaintext values entered for a new profile
// remain authoritative and are restored after decrypting the saved fields.
func veridianHydrateEmailProviderSecrets(provider *domain.EmailProvider, secretKey string) error {
	if provider == nil {
		return nil
	}

	var smtpPassword, oauthClientSecret, oauthRefreshToken string
	if provider.SMTP != nil {
		smtpPassword = provider.SMTP.Password
		oauthClientSecret = provider.SMTP.OAuth2ClientSecret
		oauthRefreshToken = provider.SMTP.OAuth2RefreshToken
	}

	if err := provider.DecryptSecretKeys(secretKey); err != nil {
		return err
	}

	if provider.SMTP != nil {
		if smtpPassword != "" {
			provider.SMTP.Password = smtpPassword
		}
		if oauthClientSecret != "" {
			provider.SMTP.OAuth2ClientSecret = oauthClientSecret
		}
		if oauthRefreshToken != "" {
			provider.SMTP.OAuth2RefreshToken = oauthRefreshToken
		}
	}

	return nil
}
