package service

import "github.com/Notifuse/notifuse/internal/domain"

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
