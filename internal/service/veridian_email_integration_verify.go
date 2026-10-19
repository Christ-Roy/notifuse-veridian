package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asaskevich/govalidator"

	"github.com/Notifuse/notifuse/internal/domain"
)

// VeridianAdminVerifyEmailIntegration exécute le VRAI test de transport d'une
// intégration email vers `to` (adresse libre) puis pose
// VeridianTransportVerifiedAt, SEULEMENT si le transport a accepté le message.
// Appelé par la route admin HMAC Hub (jamais par un owner/member : pas de
// contexte utilisateur ici, l'autorité est le secret HMAC). Ne renvoie aucun
// secret : l'appelant ne reçoit que nil ou une erreur sans identifiant.
func (s *EmailService) VeridianAdminVerifyEmailIntegration(ctx context.Context, workspaceID, integrationID, to string) error {
	to = strings.TrimSpace(to)
	if workspaceID == "" || integrationID == "" || !govalidator.IsEmail(to) {
		return domain.ValidationError{Message: "workspace_id, integration_id and a valid to are required"}
	}
	workspace, err := s.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to load workspace: %w", err)
	}
	integration := workspace.GetIntegrationByID(integrationID)
	if integration == nil || integration.Type != domain.IntegrationTypeEmail {
		return domain.ValidationError{Message: "email integration not found"}
	}
	provider := integration.EmailProvider
	if err := veridianHydrateEmailProviderSecrets(&provider, s.secretKey); err != nil {
		return fmt.Errorf("failed to load provider credentials for test: %w", err)
	}
	if err := s.testEmailProviderTransport(ctx, workspaceID, integrationID, provider, to); err != nil {
		return err
	}
	verifiedAt := time.Now().UTC()
	integration.EmailProvider.VeridianTransportVerifiedAt = &verifiedAt
	if err := s.workspaceRepo.Update(ctx, workspace); err != nil {
		return fmt.Errorf("test succeeded but verification status could not be saved: %w", err)
	}
	return nil
}
