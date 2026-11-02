package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
)

// exportWorkspaceFixture : un workspace déchiffré avec un SMTP, un IMAP et
// cinq intégrations hors liste blanche portant des secrets sentinelles.
func exportWorkspaceFixture() *domain.Workspace {
	return &domain.Workspace{ID: "ws-export", Integrations: []domain.Integration{
		{
			ID: "smtp1", Name: "Relais SMTP", Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind:    domain.EmailProviderKindSMTP,
				Senders: []domain.EmailSender{{ID: "s1", Email: "contact@client.fr", Name: "Client"}},
				SMTP: &domain.SMTPSettings{
					Host: "smtp.client.fr", Port: 587, Username: "user-smtp",
					Password: "SMTP-PW-CLAIR", UseTLS: true, AuthType: "basic",
					EHLOHostname: "mail.client.fr", SkipTLSVerify: false,
					EncryptedPassword: "cipher-ignored-because-clear-is-set",
				},
			},
		},
		{
			ID: "imap1", Name: "Boite retour", Type: domain.IntegrationTypeIMAP,
			IMAPSettings: &domain.IMAPSettings{
				Host: "imap.client.fr", Port: 993, Username: "user-imap",
				Password: "IMAP-PW-CLAIR", UseTLS: true, Folder: "INBOX", TLSServerName: "imap.client.fr",
			},
		},
		{
			ID: "ses1", Name: "SES", Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind: domain.EmailProviderKindSES,
				SES:  &domain.AmazonSESSettings{Region: "eu-west-1", AccessKey: "AK", SecretKey: "SES-SECRET-CLAIR"},
			},
		},
		{
			ID: "sg1", Name: "SendGrid", Type: domain.IntegrationTypeEmail,
			EmailProvider: domain.EmailProvider{
				Kind:     domain.EmailProviderKindSendGrid,
				SendGrid: &domain.SendGridSettings{APIKey: "SG-SECRET-CLAIR"},
			},
		},
		{
			ID: "llm1", Name: "LLM", Type: domain.IntegrationTypeLLM,
			LLMProvider: &domain.LLMProvider{Anthropic: &domain.AnthropicSettings{APIKey: "LLM-SECRET-CLAIR"}},
		},
		{
			ID: "sb1", Name: "Supabase", Type: domain.IntegrationTypeSupabase,
			SupabaseSettings: &domain.SupabaseIntegrationSettings{
				AuthEmailHook: domain.SupabaseAuthEmailHookSettings{SignatureKey: "SUPA-SECRET-CLAIR"},
			},
		},
		{
			ID: "fc1", Name: "Firecrawl", Type: domain.IntegrationTypeFirecrawl,
			FirecrawlSettings: &domain.FirecrawlSettings{APIKey: "FC-SECRET-CLAIR"},
		},
	}}
}

// exportSecretSentinels : aucune de ces valeurs ne doit sortir pour un type non exportable.
var exportSecretSentinels = []string{"SES-SECRET-CLAIR", "SG-SECRET-CLAIR", "LLM-SECRET-CLAIR", "SUPA-SECRET-CLAIR", "FC-SECRET-CLAIR"}

func TestVeridianBuildIntegrationExport_SMTPAndIMAPInClear(t *testing.T) {
	out, err := veridianBuildIntegrationExport(exportWorkspaceFixture(), "")
	require.NoError(t, err)
	require.Equal(t, "ws-export", out.WorkspaceID)
	require.Len(t, out.Exported, 2)

	smtp := out.Exported[0]
	assert.Equal(t, "smtp1", smtp.IntegrationID)
	assert.Equal(t, "smtp", smtp.Type)
	assert.Equal(t, "smtp.client.fr", smtp.Host)
	assert.Equal(t, 587, smtp.Port)
	assert.Equal(t, "user-smtp", smtp.Username)
	assert.Equal(t, "SMTP-PW-CLAIR", smtp.Password)
	assert.True(t, smtp.UseTLS)
	assert.Equal(t, "basic", smtp.AuthType)
	assert.Nil(t, smtp.OAuth2, "pas de bloc OAuth2 en basic")
	require.Len(t, smtp.Senders, 1)
	assert.Equal(t, "contact@client.fr", smtp.Senders[0].Email)

	imap := out.Exported[1]
	assert.Equal(t, "imap", imap.Type)
	assert.Equal(t, "imap.client.fr", imap.Host)
	assert.Equal(t, 993, imap.Port)
	assert.Equal(t, "user-imap", imap.Username)
	assert.Equal(t, "IMAP-PW-CLAIR", imap.Password)
	assert.Equal(t, "INBOX", imap.Folder)
	assert.Equal(t, "imap.client.fr", imap.TLSServerName)

	assert.Len(t, out.NotExportable, 5)
}

func TestVeridianBuildIntegrationExport_NonExportableNeverLeaksSecrets(t *testing.T) {
	out, err := veridianBuildIntegrationExport(exportWorkspaceFixture(), "")
	require.NoError(t, err)

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	for _, secret := range exportSecretSentinels {
		assert.NotContains(t, string(raw), secret, "un secret hors liste blanche sort dans l'export")
	}
	// Les autres secrets de SMTP/IMAP restent présents : le test sait voir une fuite.
	assert.Contains(t, string(raw), "SMTP-PW-CLAIR")
	assert.Contains(t, string(raw), "IMAP-PW-CLAIR")

	byID := map[string]VeridianNotExportable{}
	for _, ne := range out.NotExportable {
		byID[ne.IntegrationID] = ne
	}
	assert.Equal(t, "ses", byID["ses1"].Kind)
	assert.Equal(t, "llm", byID["llm1"].Type)
	assert.Contains(t, byID["fc1"].Reason, "seuls SMTP et IMAP")
}

func TestVeridianBuildIntegrationExport_SingleIntegrationFilter(t *testing.T) {
	ws := exportWorkspaceFixture()

	out, err := veridianBuildIntegrationExport(ws, "imap1")
	require.NoError(t, err)
	require.Len(t, out.Exported, 1)
	assert.Equal(t, "imap1", out.Exported[0].IntegrationID)
	assert.Empty(t, out.NotExportable)

	out, err = veridianBuildIntegrationExport(ws, "llm1")
	require.NoError(t, err)
	assert.Empty(t, out.Exported)
	require.Len(t, out.NotExportable, 1)
	assert.Equal(t, "llm1", out.NotExportable[0].IntegrationID)

	_, err = veridianBuildIntegrationExport(ws, "inconnue")
	assert.True(t, errors.Is(err, ErrVeridianExportIntegrationNotFound))
}

func TestVeridianBuildIntegrationExport_FailsClosedWhenCredentialNotDecrypted(t *testing.T) {
	ws := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{{
		ID: "smtp1", Name: "SMTP", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{
			Host: "h", Port: 25, EncryptedPassword: "ciphertext-non-dechiffre",
		}},
	}}}
	_, err := veridianBuildIntegrationExport(ws, "")
	assert.ErrorIs(t, err, errVeridianExportNotDecrypted)

	imapWS := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{{
		ID: "imap1", Name: "IMAP", Type: domain.IntegrationTypeIMAP,
		IMAPSettings: &domain.IMAPSettings{Host: "h", Port: 993, EncryptedPassword: "ciphertext"},
	}}}
	_, err = veridianBuildIntegrationExport(imapWS, "")
	assert.ErrorIs(t, err, errVeridianExportNotDecrypted)
}

func TestVeridianBuildIntegrationExport_OAuth2BlockOnlyWhenConfigured(t *testing.T) {
	ws := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{{
		ID: "smtp1", Name: "Gmail OAuth", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{
			Host: "smtp.gmail.com", Port: 587, AuthType: "oauth2", Username: "u@gmail.com",
			OAuth2Provider: "google", OAuth2ClientID: "cid", OAuth2ClientSecret: "OAUTH-CS-CLAIR",
			OAuth2RefreshToken: "OAUTH-RT-CLAIR",
		}},
	}}}
	out, err := veridianBuildIntegrationExport(ws, "")
	require.NoError(t, err)
	require.Len(t, out.Exported, 1)
	require.NotNil(t, out.Exported[0].OAuth2)
	assert.Equal(t, "google", out.Exported[0].OAuth2.Provider)
	assert.Equal(t, "cid", out.Exported[0].OAuth2.ClientID)
	assert.Equal(t, "OAUTH-CS-CLAIR", out.Exported[0].OAuth2.ClientSecret)
	assert.Equal(t, "OAUTH-RT-CLAIR", out.Exported[0].OAuth2.RefreshToken)
}

func TestVeridianBuildIntegrationExport_NilWorkspaceIsValidationError(t *testing.T) {
	_, err := veridianBuildIntegrationExport(nil, "")
	var v domain.ValidationError
	assert.True(t, errors.As(err, &v))
}

func TestVeridianAdminExportIntegrations(t *testing.T) {
	ctx := context.Background()

	t.Run("workspace_id vide : refus sans lecture", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mocks.NewMockWorkspaceRepository(ctrl)
		s := &EmailService{workspaceRepo: repo}
		_, err := s.VeridianAdminExportIntegrations(ctx, "  ", "")
		var v domain.ValidationError
		assert.True(t, errors.As(err, &v))
	})

	t.Run("workspace charge puis exporte", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mocks.NewMockWorkspaceRepository(ctrl)
		repo.EXPECT().GetByID(gomock.Any(), "ws-export").Return(exportWorkspaceFixture(), nil)
		s := &EmailService{workspaceRepo: repo}
		out, err := s.VeridianAdminExportIntegrations(ctx, "ws-export", "smtp1")
		require.NoError(t, err)
		require.Len(t, out.Exported, 1)
		assert.Equal(t, "SMTP-PW-CLAIR", out.Exported[0].Password)
	})

	t.Run("workspace absent : erreur propagee pour le 404", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mocks.NewMockWorkspaceRepository(ctrl)
		repo.EXPECT().GetByID(gomock.Any(), "absent").Return(nil, &domain.ErrWorkspaceNotFound{WorkspaceID: "absent"})
		s := &EmailService{workspaceRepo: repo}
		_, err := s.VeridianAdminExportIntegrations(ctx, "absent", "")
		var nf *domain.ErrWorkspaceNotFound
		assert.True(t, errors.As(err, &nf))
		assert.False(t, strings.Contains(err.Error(), "SMTP-PW-CLAIR"))
	})
}
