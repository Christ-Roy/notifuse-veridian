package service

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestVeridianPreserveEmailProviderSecrets(t *testing.T) {
	t.Run("keeps Gmail app password when an SMTP profile is edited", func(t *testing.T) {
		current := domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			SMTP: &domain.SMTPSettings{
				EncryptedPassword: "encrypted-app-password",
			},
		}
		next := domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			SMTP: &domain.SMTPSettings{Host: "smtp.gmail.com", Port: 587, UseTLS: true},
		}

		veridianPreserveEmailProviderSecrets(&next, current)

		require.Equal(t, "encrypted-app-password", next.SMTP.EncryptedPassword)
	})

	t.Run("new plaintext password replaces the saved one", func(t *testing.T) {
		current := domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			SMTP: &domain.SMTPSettings{EncryptedPassword: "old-ciphertext"},
		}
		next := domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			SMTP: &domain.SMTPSettings{Password: "new-app-password"},
		}

		veridianPreserveEmailProviderSecrets(&next, current)

		require.Empty(t, next.SMTP.EncryptedPassword)
		require.Equal(t, "new-app-password", next.SMTP.Password)
	})
}

func TestVeridianHydrateEmailProviderSecrets(t *testing.T) {
	const secretKey = "test-secret-key"
	smtp := &domain.SMTPSettings{Password: "gmail-app-password"}
	require.NoError(t, smtp.EncryptPassword(secretKey))
	smtp.Password = ""

	provider := domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: smtp}
	require.NoError(t, veridianHydrateEmailProviderSecrets(&provider, secretKey))
	require.Equal(t, "gmail-app-password", provider.SMTP.Password)
}

func TestVeridianEmailProviderTransportChanged(t *testing.T) {
	senders := []domain.EmailSender{{ID: "s1", Email: "owner@gmail.com", Name: "Owner", IsDefault: true}}
	current := domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, Senders: senders, SMTP: &domain.SMTPSettings{
		Host: "smtp.gmail.com", Port: 587, Username: "owner@gmail.com", UseTLS: true, EncryptedPassword: "saved",
	}}
	next := domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, Senders: senders, SMTP: &domain.SMTPSettings{
		Host: "smtp.gmail.com", Port: 587, Username: "owner@gmail.com", UseTLS: true, HasPassword: true,
	}}
	require.False(t, veridianEmailProviderTransportChanged(&next, &current), "response flags are not transport state")
	next.SMTP.Password = "new-app-password"
	require.True(t, veridianEmailProviderTransportChanged(&next, &current))
}
