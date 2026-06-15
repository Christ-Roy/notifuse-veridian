package service

// === Veridian patch ===
// Service de scoring de délivrabilité (spam score) sur un template cold rendu
// (ticket todo/2026-06-15-linter-deliverabilite-spam-score-templates.md).
//
// Gardien de sécurité : authentifie l'user pour le workspace demandé et exige la
// permission templates:read AVANT de scorer. Le scoring lui-même ne touche
// aucune donnée du workspace (le package pkg/veridian_deliverability est pur),
// mais on garde le même gardien que le breakdown R1 : un endpoint console ne doit
// pas répondre à un user non membre du workspace. Le middleware HTTP RequireAuth
// ne valide que le JWT ; c'est ici qu'on vérifie l'appartenance au workspace.

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	deliverability "github.com/Notifuse/notifuse/pkg/veridian_deliverability"
)

type veridianDeliverabilityScoreService struct {
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianDeliverabilityScoreService construit le service de scoring.
func NewVeridianDeliverabilityScoreService(
	authService domain.AuthService,
	log logger.Logger,
) domain.VeridianDeliverabilityScoreService {
	return &veridianDeliverabilityScoreService{
		authService: authService,
		logger:      log,
	}
}

// Score authentifie + vérifie la permission templates:read, puis délègue au
// linter pur. Aucune erreur n'est jamais remontée par le linter (fonction pure),
// donc le seul échec possible est l'auth/permission.
func (s *veridianDeliverabilityScoreService) Score(
	ctx context.Context,
	req *domain.VeridianDeliverabilityScoreRequest,
) (*deliverability.Result, error) {
	if req == nil || req.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	_ = ctx

	if !userWorkspace.HasPermission(domain.PermissionResourceTemplates, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceTemplates,
			domain.PermissionTypeRead,
			"Insufficient permissions: read access to templates required",
		)
	}

	result := deliverability.Score(req.ToLinterInput())
	return &result, nil
}
