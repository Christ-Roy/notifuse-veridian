package service

// === Veridian patch ===
// Service du breakdown contacts par classe de provider (R1, ticket
// todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md).
//
// Gardien de sécurité : authentifie l'user pour le workspace demandé et exige
// la permission contacts:read AVANT tout accès données (même contrat que
// ContactService.GetContacts). Le middleware HTTP RequireAuth ne valide que le
// JWT ; c'est ici qu'on vérifie l'appartenance au workspace — sans ça, un user
// authentifié pourrait lire le breakdown d'un workspace dont il n'est pas membre.

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianContactBreakdownService struct {
	repo        domain.VeridianContactProviderBreakdownRepository
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianContactBreakdownService construit le service de breakdown.
func NewVeridianContactBreakdownService(
	repo domain.VeridianContactProviderBreakdownRepository,
	authService domain.AuthService,
	log logger.Logger,
) domain.VeridianContactProviderBreakdownService {
	return &veridianContactBreakdownService{
		repo:        repo,
		authService: authService,
		logger:      log,
	}
}

// GetProviderBreakdown authentifie + vérifie la permission contacts:read, puis
// agrège le nombre de contacts par classe de provider destinataire.
func (s *veridianContactBreakdownService) GetProviderBreakdown(
	ctx context.Context,
	req *domain.VeridianProviderBreakdownRequest,
) (*domain.VeridianProviderBreakdown, error) {
	if req == nil || req.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceContacts, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceContacts,
			domain.PermissionTypeRead,
			"Insufficient permissions: read access to contacts required",
		)
	}

	counts, err := s.repo.GetProviderClassCounts(ctx, req.WorkspaceID, req.ListID)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to fetch provider breakdown counts")
		return nil, fmt.Errorf("failed to fetch provider breakdown: %w", err)
	}

	return domain.VeridianAggregateProviderBreakdownCounts(counts), nil
}
