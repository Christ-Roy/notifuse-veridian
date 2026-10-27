package service

// === Veridian — file d'envoi : lister et annuler (2026-10-27) ===
// Auth du workspace, droit automations:read (lecture) ou automations:write
// (annulation), validation, puis repository. L'acteur de l'annulation vient de la
// session, jamais du corps de la requête.

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianQueueEntriesService struct {
	repo        domain.VeridianQueueEntriesRepository
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianQueueEntriesService assemble le service.
func NewVeridianQueueEntriesService(repo domain.VeridianQueueEntriesRepository, authService domain.AuthService, log logger.Logger) domain.VeridianQueueEntriesService {
	return &veridianQueueEntriesService{repo: repo, authService: authService, logger: log}
}

func (s *veridianQueueEntriesService) authorize(ctx context.Context, workspaceID string, write bool) (context.Context, *domain.User, error) {
	ctx, user, membership, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	perm := domain.PermissionTypeRead
	if write {
		perm = domain.PermissionTypeWrite
	}
	if !membership.HasPermission(domain.PermissionResourceAutomations, perm) {
		return ctx, nil, domain.NewPermissionError(domain.PermissionResourceAutomations, perm,
			fmt.Sprintf("Insufficient permissions: %s access to automations required", perm))
	}
	return ctx, user, nil
}

// ListEntries lit une page de la file d'une automation.
func (s *veridianQueueEntriesService) ListEntries(ctx context.Context, f domain.VeridianQueueEntriesFilter) (*domain.VeridianQueueEntriesPage, error) {
	ctx, _, err := s.authorize(ctx, f.WorkspaceID, false)
	if err != nil {
		return nil, err
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	page, err := s.repo.ListEntries(ctx, f)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to list queue entries")
		return nil, fmt.Errorf("failed to list queue entries: %w", err)
	}
	return page, nil
}

// CancelEntries annule des entrées (ou simule, en dry-run). Motif obligatoire, journal écrit.
func (s *veridianQueueEntriesService) CancelEntries(ctx context.Context, req domain.VeridianQueueCancelRequest) (*domain.VeridianQueueCancelResult, error) {
	ctx, user, err := s.authorize(ctx, req.WorkspaceID, true)
	if err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	actor := ""
	if user != nil {
		actor = user.Email
	}
	res, err := s.repo.CancelEntries(ctx, req, actor)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to cancel queue entries")
		return nil, fmt.Errorf("failed to cancel queue entries: %w", err)
	}
	return res, nil
}
