package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Veridian fork, lot 5 (08/10/2026) : agrégats du tableau de bord de prospection
// (réponses par séquence et par liste, avancement des séquences, stock par liste).
// Gardien : authentifie l'utilisateur pour le workspace et exige la lecture des
// contacts (le signal « a répondu » est une donnée contact) ET de l'historique des
// messages (les envois de la fenêtre), AVANT tout accès aux données.

type veridianProspectionStatsService struct {
	repo        domain.VeridianProspectionStatsRepository
	authService domain.AuthService
	logger      logger.Logger
	now         func() time.Time
}

func NewVeridianProspectionStatsService(repo domain.VeridianProspectionStatsRepository, auth domain.AuthService, log logger.Logger) domain.VeridianProspectionStatsService {
	return &veridianProspectionStatsService{repo: repo, authService: auth, logger: log, now: time.Now}
}

func (s *veridianProspectionStatsService) GetProspectionStats(ctx context.Context, req *domain.VeridianProspectionStatsRequest) (*domain.VeridianProspectionStats, error) {
	if req == nil || req.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}
	ctx, _, membership, err := s.authService.AuthenticateUserForWorkspace(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	if !membership.HasPermission(domain.PermissionResourceContacts, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(domain.PermissionResourceContacts, domain.PermissionTypeRead, "Insufficient permissions: read access to contacts required")
	}
	if !membership.HasPermission(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "Insufficient permissions: read access to message history required")
	}
	raw, err := s.repo.GetProspectionRaw(ctx, req.WorkspaceID, req.Since, req.Until)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to read prospection stats")
		return nil, fmt.Errorf("failed to fetch prospection stats: %w", err)
	}
	return domain.VeridianBuildProspectionStats(raw, req.Since, req.Until, s.now()), nil
}
