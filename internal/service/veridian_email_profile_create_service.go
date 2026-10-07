package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

// Veridian fork — lot 3 « page Profils d'envoi » (08/10/2026).
//
// emailProfiles.create : un profil SMTP et, si demandée, sa boîte IMAP liée,
// écrits dans UN SEUL workspaceRepo.Update. Tout est validé (et les secrets
// chiffrés) en mémoire avant l'écriture : une erreur ne laisse rien derrière
// elle. Réservé au propriétaire du workspace, comme createIntegration.
// Le profil naît hors rotation, non vérifié ; le secret n'est jamais renvoyé.

type veridianEmailProfileCreateService struct {
	repo        domain.WorkspaceRepository
	authService domain.AuthService
	secretKey   string
	logger      logger.Logger
}

func NewVeridianEmailProfileCreateService(
	repo domain.WorkspaceRepository,
	auth domain.AuthService,
	secretKey string,
	log logger.Logger,
) domain.VeridianEmailProfileCreateService {
	return &veridianEmailProfileCreateService{repo: repo, authService: auth, secretKey: secretKey, logger: log}
}

func (s *veridianEmailProfileCreateService) CreateEmailProfile(ctx context.Context, req domain.VeridianCreateEmailProfileRequest) (*domain.VeridianCreateEmailProfileResult, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.NewValidationError(err.Error())
	}
	ctx, user, _, err := s.authService.AuthenticateUserForWorkspace(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	userWorkspace, err := s.repo.GetUserWorkspace(ctx, user.ID, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if userWorkspace.Role != "owner" {
		return nil, &domain.ErrUnauthorized{Message: "user is not an owner of the workspace"}
	}
	workspace, err := s.repo.GetByID(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}

	provider, imap, err := veridianBuildProfileFromRequest(req)
	if err != nil {
		return nil, domain.NewValidationError(err.Error())
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.SenderEmail)
	}
	now := time.Now().UTC()
	result := &domain.VeridianCreateEmailProfileResult{IntegrationID: uuid.New().String()}

	if imap != nil {
		result.IMAPIntegrationID = uuid.New().String()
		inbox := domain.Integration{
			ID: result.IMAPIntegrationID, Name: name + " (IMAP)", Type: domain.IntegrationTypeIMAP,
			IMAPSettings: imap, CreatedAt: now, UpdatedAt: now,
		}
		if err := inbox.Validate(s.secretKey); err != nil {
			return nil, domain.NewValidationError(err.Error())
		}
		workspace.AddIntegration(inbox)
		provider.VeridianReturnIMAPIntegrationID = result.IMAPIntegrationID
	}

	integration := domain.Integration{
		ID: result.IntegrationID, Name: name, Type: domain.IntegrationTypeEmail,
		EmailProvider: *provider, CreatedAt: now, UpdatedAt: now,
	}
	if err := integration.Validate(s.secretKey); err != nil {
		return nil, domain.NewValidationError(err.Error())
	}
	if err := workspace.ValidateVeridianReturnIMAPLink(&integration.EmailProvider); err != nil {
		return nil, err
	}
	workspace.AddIntegration(integration)

	if err := s.repo.Update(ctx, workspace); err != nil {
		s.logger.WithField("workspace_id", req.WorkspaceID).WithField("error", err.Error()).Error("Failed to save new email profile")
		return nil, err
	}
	return result, nil
}

// veridianBuildProfileFromRequest assemble le transport et la boîte de retour.
// Fonction pure, testée sans base. Gmail : hôtes et ports imposés, plafond 30
// par défaut, cadence technique 1 par minute. SMTP : 60 par minute.
func veridianBuildProfileFromRequest(req domain.VeridianCreateEmailProfileRequest) (*domain.EmailProvider, *domain.IMAPSettings, error) {
	email := strings.ToLower(strings.TrimSpace(req.SenderEmail))
	sender := domain.EmailSender{ID: uuid.New().String(), Email: email, Name: strings.TrimSpace(req.SenderName), IsDefault: true}

	switch req.Type {
	case domain.VeridianCreateProfileTypeGmailAppPassword:
		password := strings.ReplaceAll(req.AppPassword, " ", "")
		cap := req.ProfileDailyCap
		if cap <= 0 {
			cap = domain.VeridianGmailDefaultDailyCap
		}
		provider := &domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			SMTP: &domain.SMTPSettings{
				Host: domain.VeridianGmailSMTPHost, Port: domain.VeridianGmailSMTPPort, UseTLS: true,
				AuthType: "basic", Username: email, Password: password,
			},
			Senders:                   []domain.EmailSender{sender},
			RateLimitPerMinute:        domain.VeridianCreateProfileGmailRatePerMinute,
			VeridianProfileDailyCap:   cap,
			VeridianGmailAccountType:  req.GmailAccountType,
		}
		imap := &domain.IMAPSettings{
			Host: domain.VeridianGmailIMAPHost, Port: domain.VeridianGmailIMAPPort, UseTLS: true,
			Username: email, Password: password, Folder: domain.DefaultIMAPFolder,
		}
		return provider, imap, nil
	case domain.VeridianCreateProfileTypeSMTPIMAP:
		provider := &domain.EmailProvider{
			Kind: domain.EmailProviderKindSMTP,
			SMTP: &domain.SMTPSettings{
				Host: strings.TrimSpace(req.SMTP.Host), Port: req.SMTP.Port, UseTLS: req.SMTP.UseTLS,
				AuthType: "basic", Username: strings.TrimSpace(req.SMTP.Username), Password: req.SMTP.Password,
			},
			Senders:                 []domain.EmailSender{sender},
			RateLimitPerMinute:      domain.VeridianCreateProfileDefaultRatePerMinute,
			VeridianProfileDailyCap: req.ProfileDailyCap,
		}
		if req.IMAP == nil {
			return provider, nil, nil
		}
		folder := strings.TrimSpace(req.IMAP.Folder)
		if folder == "" {
			folder = domain.DefaultIMAPFolder
		}
		imap := &domain.IMAPSettings{
			Host: strings.TrimSpace(req.IMAP.Host), Port: req.IMAP.Port, UseTLS: req.IMAP.UseTLS,
			Username: strings.TrimSpace(req.IMAP.Username), Password: req.IMAP.Password, Folder: folder,
		}
		return provider, imap, nil
	}
	return nil, nil, fmt.Errorf("unsupported profile type: %q", req.Type)
}
