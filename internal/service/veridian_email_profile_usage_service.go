package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianEmailProfileUsageService struct {
	repo          domain.VeridianEmailProfileUsageRepository
	workspaceRepo domain.WorkspaceRepository
	authService   domain.AuthService
	logger        logger.Logger
}

func NewVeridianEmailProfileUsageService(repo domain.VeridianEmailProfileUsageRepository, workspaceRepo domain.WorkspaceRepository, auth domain.AuthService, log logger.Logger) domain.VeridianEmailProfileUsageService {
	return &veridianEmailProfileUsageService{repo: repo, workspaceRepo: workspaceRepo, authService: auth, logger: log}
}

func (s *veridianEmailProfileUsageService) GetEmailProfilesUsage(ctx context.Context, workspaceID string) (*domain.VeridianEmailProfilesUsage, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}
	ctx, _, membership, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	if !membership.HasPermission(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "Insufficient permissions: read access to message history required")
	}
	workspace, err := s.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := s.repo.GetEmailProfileUsage(ctx, workspaceID, since)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to fetch email profile usage")
		return nil, fmt.Errorf("failed to fetch email profile usage: %w", err)
	}
	return domain.VeridianAggregateEmailProfileUsage(workspace, rows, now), nil
}
