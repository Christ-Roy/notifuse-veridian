package service

import (
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func authzKeyWith(workspaceWrite, workspaceRead bool) *domain.UserWorkspace {
	return &domain.UserWorkspace{Role: "member", Permissions: domain.UserPermissions{
		domain.PermissionResourceWorkspace: {Read: workspaceRead, Write: workspaceWrite},
	}}
}

func TestVeridianIntegrationWriteAllowed(t *testing.T) {
	owner := &domain.UserWorkspace{Role: "owner"}
	cases := []struct {
		name string
		uw   *domain.UserWorkspace
		typ  domain.IntegrationType
		ok   bool
	}{
		{"owner : tous les types", owner, domain.IntegrationTypeSupabase, true},
		{"cle en ecriture : email", authzKeyWith(true, true), domain.IntegrationTypeEmail, true},
		{"cle en ecriture : imap", authzKeyWith(true, true), domain.IntegrationTypeIMAP, true},
		{"cle en ecriture : type encore inconnu", authzKeyWith(true, true), "", true},
		{"cle en ecriture : llm reste owner", authzKeyWith(true, true), domain.IntegrationTypeLLM, false},
		{"cle en ecriture : firecrawl reste owner", authzKeyWith(true, true), domain.IntegrationTypeFirecrawl, false},
		{"cle en ecriture : supabase reste owner", authzKeyWith(true, true), domain.IntegrationTypeSupabase, false},
		{"cle en lecture seule : refusee", authzKeyWith(false, true), domain.IntegrationTypeEmail, false},
		{"membre sans permission workspace : refuse", &domain.UserWorkspace{Role: "member"}, domain.IntegrationTypeIMAP, false},
		{"absent : refuse", nil, domain.IntegrationTypeEmail, false},
		{"role inconnu : refuse", &domain.UserWorkspace{Role: "viewer", Permissions: domain.FullPermissions}, domain.IntegrationTypeEmail, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := veridianIntegrationWriteAllowed(c.uw, c.typ)
			if c.ok {
				require.NoError(t, err)
				return
			}
			var denied *domain.ErrUnauthorized
			require.ErrorAs(t, err, &denied)
		})
	}
}

func TestVeridianAuthorizeIntegrationCreate_OnlySMTPForNonOwner(t *testing.T) {
	key := authzKeyWith(true, true)
	smtp := &domain.CreateIntegrationRequest{Type: domain.IntegrationTypeEmail, Provider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}}
	ses := &domain.CreateIntegrationRequest{Type: domain.IntegrationTypeEmail, Provider: domain.EmailProvider{Kind: domain.EmailProviderKindSES}}
	require.NoError(t, veridianAuthorizeIntegrationCreate(key, smtp))
	assert.Error(t, veridianAuthorizeIntegrationCreate(key, ses))
	require.NoError(t, veridianAuthorizeIntegrationCreate(&domain.UserWorkspace{Role: "owner"}, ses))
}

func TestVeridianAuthorizeIntegrationUpdate_SecretsNeverRebound(t *testing.T) {
	key := authzKeyWith(true, true)
	existing := lot2SMTPIntegration("p")
	existing.EmailProvider.SMTP.Password = ""
	existing.EmailProvider.SMTP.EncryptedPassword = "cipher"

	update := func(mutate func(*domain.SMTPSettings)) *domain.UpdateIntegrationRequest {
		s := *existing.EmailProvider.SMTP
		s.EncryptedPassword = "" // la reponse API ne renvoie jamais le chiffre
		mutate(&s)
		p := existing.EmailProvider
		p.SMTP = &s
		return &domain.UpdateIntegrationRequest{WorkspaceID: "ws", IntegrationID: "p", Provider: p}
	}

	t.Run("meme destination sans mot de passe : accepte (aller-retour integrations:cold)", func(t *testing.T) {
		require.NoError(t, veridianAuthorizeIntegrationUpdate(key, &existing, update(func(*domain.SMTPSettings) {}), "k"))
	})
	t.Run("hote change sans mot de passe : refuse (le chiffre stocke irait chez l'appelant)", func(t *testing.T) {
		err := veridianAuthorizeIntegrationUpdate(key, &existing, update(func(s *domain.SMTPSettings) { s.Host = "smtp.evil.example" }), "k")
		var v domain.ValidationError
		require.ErrorAs(t, err, &v)
	})
	t.Run("port ou identifiant change sans mot de passe : refuse", func(t *testing.T) {
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, &existing, update(func(s *domain.SMTPSettings) { s.Port = 25 }), "k"))
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, &existing, update(func(s *domain.SMTPSettings) { s.Username = "autre" }), "k"))
	})
	t.Run("hote change AVEC le mot de passe : accepte", func(t *testing.T) {
		require.NoError(t, veridianAuthorizeIntegrationUpdate(key, &existing, update(func(s *domain.SMTPSettings) {
			s.Host, s.Password = "smtp.new.example", "nouveau"
		}), "k"))
	})
	t.Run("owner : jamais bloque", func(t *testing.T) {
		require.NoError(t, veridianAuthorizeIntegrationUpdate(&domain.UserWorkspace{Role: "owner"}, &existing, update(func(s *domain.SMTPSettings) { s.Host = "x.example" }), "k"))
	})
	t.Run("fournisseur non SMTP : owner seulement", func(t *testing.T) {
		ses := lot2SMTPIntegration("s")
		ses.EmailProvider.Kind = domain.EmailProviderKindSES
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, &ses, &domain.UpdateIntegrationRequest{Provider: ses.EmailProvider}, "k"))
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, &existing, &domain.UpdateIntegrationRequest{Provider: domain.EmailProvider{Kind: domain.EmailProviderKindSES}}, "k"))
	})
	t.Run("IMAP : hote change sans mot de passe refuse, avec mot de passe accepte", func(t *testing.T) {
		imap := domain.Integration{ID: "i", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "imap.a.example", Port: 993, Username: "u", EncryptedPassword: "cipher"}}
		same := &domain.UpdateIntegrationRequest{IMAPSettings: &domain.IMAPSettings{Host: "imap.a.example", Port: 993, Username: "u"}}
		moved := &domain.UpdateIntegrationRequest{IMAPSettings: &domain.IMAPSettings{Host: "imap.evil.example", Port: 993, Username: "u"}}
		movedWithPassword := &domain.UpdateIntegrationRequest{IMAPSettings: &domain.IMAPSettings{Host: "imap.b.example", Port: 993, Username: "u", Password: "nouveau"}}
		require.NoError(t, veridianAuthorizeIntegrationUpdate(key, &imap, same, "k"))
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, &imap, moved, "k"))
		require.NoError(t, veridianAuthorizeIntegrationUpdate(key, &imap, movedWithPassword, "k"))
	})
	t.Run("integration absente ou type owner-only", func(t *testing.T) {
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, nil, &domain.UpdateIntegrationRequest{}, "k"))
		llm := domain.Integration{ID: "l", Type: domain.IntegrationTypeLLM}
		assert.Error(t, veridianAuthorizeIntegrationUpdate(key, &llm, &domain.UpdateIntegrationRequest{}, "k"))
	})
}
