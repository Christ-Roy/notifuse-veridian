package service

// === Veridian patch ===
// Service du KPI reply rate (taux de réponse cold outbound, ticket
// todo/2026-06-16-kpi-reply-rate-dashboard.md).
//
// Gardien de sécurité : authentifie l'user pour le workspace demandé et exige
// la permission contacts:read AVANT tout accès données — même contrat que le
// breakdown contacts R1 (veridian_contact_breakdown_service.go), car le signal
// reply est une donnée CONTACT. Le middleware HTTP RequireAuth ne valide que le
// JWT ; c'est ici qu'on vérifie l'appartenance au workspace — sans ça, un user
// authentifié pourrait lire le reply count d'un workspace dont il n'est pas membre.

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianReplyStatsService struct {
	repo        domain.VeridianContactReplyRepository
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianReplyStatsService construit le service de reply stats.
func NewVeridianReplyStatsService(
	repo domain.VeridianContactReplyRepository,
	authService domain.AuthService,
	log logger.Logger,
) domain.VeridianReplyStatsService {
	return &veridianReplyStatsService{
		repo:        repo,
		authService: authService,
		logger:      log,
	}
}

// GetReplyStats authentifie + vérifie la permission contacts:read, puis compte
// les contacts ayant répondu dans la fenêtre [Since, Until[.
func (s *veridianReplyStatsService) GetReplyStats(
	ctx context.Context,
	req *domain.VeridianReplyStatsRequest,
) (*domain.VeridianReplyStats, error) {
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

	replied, err := s.repo.CountRepliedSince(ctx, req.WorkspaceID, req.Since, req.Until)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to count replied contacts")
		return nil, fmt.Errorf("failed to fetch reply stats: %w", err)
	}

	return &domain.VeridianReplyStats{Replied: replied}, nil
}
