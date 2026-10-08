package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Veridian fork, lot 4 (08/10/2026) : emailProfiles.setUsage / pause / resume.
//
// La console et le CLI changent l'usage d'un profil (commercial en rotation,
// transactionnel, hors service) et sa pause par ces trois opérations nommées,
// plus par updateIntegration ni workspaces.update. Les règles (exclusivité,
// rotation jamais vidée, profil vérifié, pas de pause sur un transactionnel) sont
// celles de domain.VeridianApplyUsage / VeridianApplyPause, appliquées à un
// workspace relu puis écrit en UNE seule fois. Droit requis : écriture sur le
// workspace (propriétaire, ou clé d'API scopée avec workspace:write).

type veridianEmailProfileAdminService struct {
	repo       domain.WorkspaceRepository
	auth       domain.AuthService
	policyRepo domain.EmailIntegrationPolicyQueueRepository
	logger     logger.Logger
	now        func() time.Time
}

func NewVeridianEmailProfileAdminService(
	repo domain.WorkspaceRepository,
	auth domain.AuthService,
	policyRepo domain.EmailIntegrationPolicyQueueRepository,
	log logger.Logger,
) domain.VeridianEmailProfileAdminService {
	return &veridianEmailProfileAdminService{repo: repo, auth: auth, policyRepo: policyRepo, logger: log, now: time.Now}
}

func (s *veridianEmailProfileAdminService) authorize(ctx context.Context, workspaceID string) (context.Context, error) {
	ctx, _, membership, err := s.auth.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return ctx, fmt.Errorf("failed to authenticate user: %w", err)
	}
	if !membership.HasPermission(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite) {
		return ctx, domain.NewPermissionError(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite, "Insufficient permissions: write access to the workspace required")
	}
	return ctx, nil
}

// wake réévalue les entrées en file des profils touchés : une pause levée, un
// profil qui entre ou sort de la rotation ou devient transactionnel ne doivent
// pas attendre un report périmé. Meilleur effort, jamais bloquant.
func (s *veridianEmailProfileAdminService) wake(ctx context.Context, workspaceID string, integrationIDs ...string) {
	if s.policyRepo == nil {
		return
	}
	seen := map[string]bool{}
	for _, id := range integrationIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := s.policyRepo.WakePendingByIntegration(ctx, workspaceID, id); err != nil {
			s.logger.WithField("workspace_id", workspaceID).WithField("integration_id", id).WithField("error", err.Error()).
				Warn("Email profile changed but pending queue wake failed")
		}
	}
}

func (s *veridianEmailProfileAdminService) SetUsage(ctx context.Context, req domain.VeridianSetUsageRequest) (*domain.VeridianSetUsageResult, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.NewValidationError(err.Error())
	}
	ctx, err := s.authorize(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	workspace, err := s.repo.GetByID(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	result, err := workspace.VeridianApplyUsage(req.IntegrationID, req.Usage)
	if err != nil {
		return nil, err
	}
	workspace.UpdatedAt = s.now().UTC()
	if err := s.repo.Update(ctx, workspace); err != nil {
		s.logger.WithField("workspace_id", req.WorkspaceID).WithField("integration_id", req.IntegrationID).WithField("error", err.Error()).
			Error("Failed to save email profile usage")
		return nil, err
	}
	s.wake(ctx, req.WorkspaceID, req.IntegrationID, result.PreviousTransactionalIntegrationID)
	return result, nil
}

func (s *veridianEmailProfileAdminService) setPaused(ctx context.Context, req domain.VeridianPauseRequest, paused bool) (*domain.VeridianPauseResult, error) {
	if err := req.Validate(); err != nil {
		return nil, domain.NewValidationError(err.Error())
	}
	ctx, err := s.authorize(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	workspace, err := s.repo.GetByID(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	result, err := workspace.VeridianApplyPause(req.IntegrationID, paused)
	if err != nil {
		return nil, err
	}
	workspace.UpdatedAt = s.now().UTC()
	if err := s.repo.Update(ctx, workspace); err != nil {
		s.logger.WithField("workspace_id", req.WorkspaceID).WithField("integration_id", req.IntegrationID).WithField("error", err.Error()).
			Error("Failed to save email profile pause state")
		return nil, err
	}
	if !paused {
		s.wake(ctx, req.WorkspaceID, req.IntegrationID)
	}
	return result, nil
}

func (s *veridianEmailProfileAdminService) Pause(ctx context.Context, req domain.VeridianPauseRequest) (*domain.VeridianPauseResult, error) {
	return s.setPaused(ctx, req, true)
}

func (s *veridianEmailProfileAdminService) Resume(ctx context.Context, req domain.VeridianPauseRequest) (*domain.VeridianPauseResult, error) {
	return s.setPaused(ctx, req, false)
}
