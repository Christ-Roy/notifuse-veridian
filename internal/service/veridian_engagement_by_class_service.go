package service

// === Veridian patch ===
// Service de l'engagement par classe de provider destinataire (KPI dashboard
// cold, ticket todo/2026-06-16-kpi-engagement-par-classe-provider.md).
//
// Gardien de sécurité : authentifie l'user pour le workspace et exige la
// permission contacts:read AVANT tout accès données — même contrat que le
// breakdown contacts R1 et le reply stats (le message_history est une donnée
// du workspace). Le middleware RequireAuth ne valide que le JWT ; c'est ici
// qu'on vérifie l'appartenance au workspace.

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianEngagementByClassService struct {
	repo        domain.VeridianEngagementByClassRepository
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianEngagementByClassService construit le service.
func NewVeridianEngagementByClassService(
	repo domain.VeridianEngagementByClassRepository,
	authService domain.AuthService,
	log logger.Logger,
) domain.VeridianEngagementByClassService {
	return &veridianEngagementByClassService{
		repo:        repo,
		authService: authService,
		logger:      log,
	}
}

// GetEngagementByClass authentifie + vérifie la permission contacts:read, puis
// agrège l'engagement par classe sur la fenêtre [Since, Until[.
func (s *veridianEngagementByClassService) GetEngagementByClass(
	ctx context.Context,
	req *domain.VeridianEngagementByClassRequest,
) (*domain.VeridianEngagementByClass, error) {
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

	rows, err := s.repo.GetEngagementByDomain(ctx, req.WorkspaceID, req.Since, req.Until)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to fetch engagement by domain")
		return nil, fmt.Errorf("failed to fetch engagement by class: %w", err)
	}

	return domain.VeridianAggregateEngagementByClass(rows), nil
}
