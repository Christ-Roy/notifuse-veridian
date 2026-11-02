package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// === Veridian — export OPÉRATEUR des identifiants SMTP et IMAP d'un workspace ===
//
// Décision Robert (02/11/2026) : exception à la rédaction des secrets, pour
// SMTP et IMAP SEULEMENT, afin que l'opérateur reconstruise ses profils
// d'envoi derrière VPN chez ses clients. Tout le reste reste masqué.
//
// Garde-fous (fiche docs/claude/52) :
//   - appelé UNIQUEMENT par la route HMAC Hub (cf. veridian_integration_export_handler.go) ;
//     aucun contexte utilisateur ici, l'autorité est le secret HMAC ;
//   - liste BLANCHE : seules les intégrations SMTP et IMAP sortent en clair. Chaque
//     autre type (SES, SendGrid, LLM, Supabase, Firecrawl, ...) est listé comme
//     « non exportable » avec son identifiant et son nom, jamais ses champs ;
//   - la réponse est une structure EXPLICITE construite champ par champ : on ne
//     s'appuie jamais sur le json.Marshal par défaut d'un domain.Integration.

// ErrVeridianExportIntegrationNotFound : l'integration_id demandé n'existe pas dans le workspace.
var ErrVeridianExportIntegrationNotFound = errors.New("integration not found")

// errVeridianExportNotDecrypted : un secret chiffré est présent mais son clair est vide.
// On échoue fermé plutôt que d'exporter un mot de passe vide qui ferait croire à une absence.
var errVeridianExportNotDecrypted = errors.New("encrypted credential present but not decrypted")

// VeridianExportSender : expéditeur d'un profil SMTP (aucun secret).
type VeridianExportSender struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// VeridianExportOAuth2 : champs OAuth2 d'un SMTP, présents seulement si configurés.
type VeridianExportOAuth2 struct {
	Provider     string `json:"provider"`
	TenantID     string `json:"tenant_id,omitempty"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// VeridianExportedIntegration : une intégration SMTP ou IMAP exportée en clair.
type VeridianExportedIntegration struct {
	IntegrationID string `json:"integration_id"`
	Name          string `json:"name"`
	Type          string `json:"type"` // "smtp" ou "imap"
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	UseTLS        bool   `json:"use_tls"`

	// SMTP uniquement.
	AuthType      string                 `json:"auth_type,omitempty"`
	EHLOHostname  string                 `json:"ehlo_hostname,omitempty"`
	SkipTLSVerify bool                   `json:"skip_tls_verify,omitempty"`
	OAuth2        *VeridianExportOAuth2  `json:"oauth2,omitempty"`
	Senders       []VeridianExportSender `json:"senders,omitempty"`

	// IMAP uniquement.
	Folder        string `json:"folder,omitempty"`
	TLSServerName string `json:"tls_server_name,omitempty"`
}

// VeridianNotExportable : une intégration présente mais hors liste blanche. Aucun champ secret.
type VeridianNotExportable struct {
	IntegrationID string `json:"integration_id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Kind          string `json:"kind,omitempty"`
	Reason        string `json:"reason"`
}

// VeridianIntegrationExport : réponse complète de l'export opérateur.
type VeridianIntegrationExport struct {
	WorkspaceID   string                        `json:"workspace_id"`
	Exported      []VeridianExportedIntegration `json:"exported"`
	NotExportable []VeridianNotExportable       `json:"not_exportable"`
}

// VeridianAdminExportIntegrations charge le workspace (credentials déchiffrés par
// le repository) puis construit l'export. integrationID vide = toutes les intégrations.
func (s *EmailService) VeridianAdminExportIntegrations(ctx context.Context, workspaceID, integrationID string) (*VeridianIntegrationExport, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	integrationID = strings.TrimSpace(integrationID)
	if workspaceID == "" {
		return nil, domain.ValidationError{Message: "workspace_id is required"}
	}
	workspace, err := s.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}
	return veridianBuildIntegrationExport(workspace, integrationID)
}

// veridianBuildIntegrationExport construit l'export à partir d'un workspace déjà
// déchiffré. Fonction pure : testable sans base ni chiffrement de bout en bout.
func veridianBuildIntegrationExport(workspace *domain.Workspace, integrationID string) (*VeridianIntegrationExport, error) {
	if workspace == nil {
		return nil, domain.ValidationError{Message: "workspace is required"}
	}
	out := &VeridianIntegrationExport{
		WorkspaceID:   workspace.ID,
		Exported:      []VeridianExportedIntegration{},
		NotExportable: []VeridianNotExportable{},
	}
	if integrationID != "" {
		integration := workspace.GetIntegrationByID(integrationID)
		if integration == nil {
			return nil, ErrVeridianExportIntegrationNotFound
		}
		if err := veridianAppendExport(out, integration); err != nil {
			return nil, err
		}
		return out, nil
	}
	for i := range workspace.Integrations {
		if err := veridianAppendExport(out, &workspace.Integrations[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// veridianAppendExport range une intégration dans exported (SMTP/IMAP) ou not_exportable.
func veridianAppendExport(out *VeridianIntegrationExport, integration *domain.Integration) error {
	switch integration.Type {
	case domain.IntegrationTypeEmail:
		provider := integration.EmailProvider
		if provider.Kind == domain.EmailProviderKindSMTP && provider.SMTP != nil {
			exported, err := veridianExportSMTP(integration, provider.SMTP, provider.Senders)
			if err != nil {
				return err
			}
			out.Exported = append(out.Exported, exported)
			return nil
		}
		out.NotExportable = append(out.NotExportable, veridianNotExportable(integration, string(provider.Kind), "fournisseur email non SMTP : non exportable"))
	case domain.IntegrationTypeIMAP:
		if integration.IMAPSettings == nil {
			out.NotExportable = append(out.NotExportable, veridianNotExportable(integration, "", "configuration IMAP absente"))
			return nil
		}
		exported, err := veridianExportIMAP(integration, integration.IMAPSettings)
		if err != nil {
			return err
		}
		out.Exported = append(out.Exported, exported)
	default:
		out.NotExportable = append(out.NotExportable, veridianNotExportable(integration, "", "type non exportable (seuls SMTP et IMAP le sont)"))
	}
	return nil
}

func veridianNotExportable(integration *domain.Integration, kind, reason string) VeridianNotExportable {
	return VeridianNotExportable{
		IntegrationID: integration.ID,
		Name:          integration.Name,
		Type:          string(integration.Type),
		Kind:          kind,
		Reason:        reason,
	}
}

// veridianExportSMTP copie EXPLICITEMENT les champs SMTP autorisés.
func veridianExportSMTP(integration *domain.Integration, s *domain.SMTPSettings, senders []domain.EmailSender) (VeridianExportedIntegration, error) {
	if (s.EncryptedPassword != "" && s.Password == "") ||
		(s.EncryptedUsername != "" && s.Username == "") ||
		(s.EncryptedOAuth2ClientSecret != "" && s.OAuth2ClientSecret == "") ||
		(s.EncryptedOAuth2RefreshToken != "" && s.OAuth2RefreshToken == "") {
		return VeridianExportedIntegration{}, errVeridianExportNotDecrypted
	}
	exported := VeridianExportedIntegration{
		IntegrationID: integration.ID,
		Name:          integration.Name,
		Type:          "smtp",
		Host:          s.Host,
		Port:          s.Port,
		Username:      s.Username,
		Password:      s.Password,
		UseTLS:        s.UseTLS,
		AuthType:      s.AuthType,
		EHLOHostname:  s.EHLOHostname,
		SkipTLSVerify: s.SkipTLSVerify,
	}
	if s.AuthType == "oauth2" || s.OAuth2Provider != "" || s.OAuth2ClientID != "" ||
		s.OAuth2ClientSecret != "" || s.OAuth2RefreshToken != "" {
		exported.OAuth2 = &VeridianExportOAuth2{
			Provider:     s.OAuth2Provider,
			TenantID:     s.OAuth2TenantID,
			ClientID:     s.OAuth2ClientID,
			ClientSecret: s.OAuth2ClientSecret,
			RefreshToken: s.OAuth2RefreshToken,
		}
	}
	for _, sender := range senders {
		exported.Senders = append(exported.Senders, VeridianExportSender{Email: sender.Email, Name: sender.Name})
	}
	return exported, nil
}

// veridianExportIMAP copie EXPLICITEMENT les champs IMAP autorisés.
func veridianExportIMAP(integration *domain.Integration, s *domain.IMAPSettings) (VeridianExportedIntegration, error) {
	if s.EncryptedPassword != "" && s.Password == "" {
		return VeridianExportedIntegration{}, errVeridianExportNotDecrypted
	}
	return VeridianExportedIntegration{
		IntegrationID: integration.ID,
		Name:          integration.Name,
		Type:          "imap",
		Host:          s.Host,
		Port:          s.Port,
		Username:      s.Username,
		Password:      s.Password,
		UseTLS:        s.UseTLS,
		Folder:        s.Folder,
		TLSServerName: s.TLSServerName,
	}, nil
}
