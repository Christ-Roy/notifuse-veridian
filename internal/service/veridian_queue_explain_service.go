package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

// Veridian fork (fiche 62, lot 1) : explorateur de file et journal des decisions.
// Gardien : authentifie l'utilisateur pour le workspace et exige automations:read
// (lecture) ou automations:write (recalcul) AVANT tout acces aux donnees.

// ErrVeridianQueueEntryNotFound : l'entree demandee n'existe plus (envoyee ou supprimee).
var ErrVeridianQueueEntryNotFound = errors.New("queue entry not found")

type veridianQueueExplainService struct {
	explainRepo   domain.VeridianQueueExplainRepository
	decisionRepo  domain.VeridianSendDecisionRepository
	workspaceRepo domain.WorkspaceRepository
	authService   domain.AuthService
	logger        logger.Logger
}

// NewVeridianQueueExplainService assemble le service.
func NewVeridianQueueExplainService(
	explainRepo domain.VeridianQueueExplainRepository,
	decisionRepo domain.VeridianSendDecisionRepository,
	workspaceRepo domain.WorkspaceRepository,
	authService domain.AuthService,
	log logger.Logger,
) domain.VeridianQueueExplainService {
	return &veridianQueueExplainService{explainRepo: explainRepo, decisionRepo: decisionRepo, workspaceRepo: workspaceRepo, authService: authService, logger: log}
}

func (s *veridianQueueExplainService) authorize(ctx context.Context, workspaceID string, write bool) (context.Context, error) {
	if workspaceID == "" {
		return ctx, fmt.Errorf("workspace_id is required")
	}
	ctx, _, membership, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return ctx, fmt.Errorf("failed to authenticate user: %w", err)
	}
	perm := domain.PermissionTypeRead
	if write {
		perm = domain.PermissionTypeWrite
	}
	if !membership.HasPermission(domain.PermissionResourceAutomations, perm) {
		return ctx, domain.NewPermissionError(domain.PermissionResourceAutomations, perm,
			fmt.Sprintf("Insufficient permissions: %s access to automations required", perm))
	}
	return ctx, nil
}

// veridianProfileName resout le nom d'un profil d'envoi (integration), "" sinon.
func veridianProfileName(ws *domain.Workspace, id string) string {
	if ws == nil || id == "" {
		return ""
	}
	if integ := ws.GetIntegrationByID(id); integ != nil && integ.Name != "" {
		return integ.Name
	}
	return ""
}

func (s *veridianQueueExplainService) loadWorkspace(ctx context.Context, workspaceID string) *domain.Workspace {
	ws, err := s.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		s.logger.WithField("error", err.Error()).Warn("queue explain: workspace lookup failed, profile names omitted")
		return nil
	}
	return ws
}

func (s *veridianQueueExplainService) Explain(ctx context.Context, workspaceID string, f domain.VeridianQueueExplainFilter) (*domain.VeridianQueueExplain, error) {
	ctx, err := s.authorize(ctx, workspaceID, false)
	if err != nil {
		return nil, err
	}
	ws := s.loadWorkspace(ctx, workspaceID)

	if f.EntryID != "" {
		detail, err := s.explainRepo.EntryDetail(ctx, workspaceID, f.EntryID)
		if err != nil {
			return nil, err
		}
		if detail == nil {
			return nil, ErrVeridianQueueEntryNotFound
		}
		detail.ProfileName = veridianProfileName(ws, detail.IntegrationID)
		decisions, _, derr := s.decisionRepo.List(ctx, workspaceID, domain.VeridianSendDecisionFilter{EntryID: f.EntryID, Limit: 1, WithTrace: true})
		if derr != nil {
			s.logger.WithField("error", derr.Error()).Warn("queue explain: last decision lookup failed")
		} else if len(decisions) > 0 {
			decisions[0].ProfileName = veridianProfileName(ws, decisions[0].ProfileID)
			detail.LastDecision = decisions[0]
		}
		return &domain.VeridianQueueExplain{
			WorkspaceID: workspaceID, GeneratedAt: time.Now().UTC(),
			Groups: []domain.VeridianQueueGroup{}, Orphans: domain.VeridianQueueOrphans{ByNode: []domain.VeridianQueueOrphanNode{}},
			Entry: detail,
		}, nil
	}

	if len(f.GroupBy) == 0 {
		f.GroupBy = []string{"automation", "node", "reason", "profile"}
	}
	allowed := map[string]bool{}
	for _, g := range domain.VeridianQueueGroupBy {
		allowed[g] = true
	}
	for _, g := range f.GroupBy {
		if !allowed[g] {
			return nil, fmt.Errorf("invalid group_by value %q (allowed: automation,node,reason,profile,class)", g)
		}
	}
	out, err := s.explainRepo.Explain(ctx, workspaceID, f)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to explain queue")
		return nil, fmt.Errorf("failed to explain queue: %w", err)
	}
	for i := range out.Groups {
		out.Groups[i].ProfileName = veridianProfileName(ws, out.Groups[i].ProfileID)
	}
	return out, nil
}

func (s *veridianQueueExplainService) Decisions(ctx context.Context, workspaceID string, f domain.VeridianSendDecisionFilter) ([]*domain.VeridianSendDecision, string, string, error) {
	ctx, err := s.authorize(ctx, workspaceID, false)
	if err != nil {
		return nil, "", "", err
	}
	if f.Outcome != "" && !domain.VeridianValidOutcome(f.Outcome) {
		return nil, "", "", fmt.Errorf("invalid outcome %q", f.Outcome)
	}
	ws := s.loadWorkspace(ctx, workspaceID)
	list, next, err := s.decisionRepo.List(ctx, workspaceID, f)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to list send decisions")
		return nil, "", "", fmt.Errorf("failed to list send decisions: %w", err)
	}
	for _, d := range list {
		d.ProfileName = veridianProfileName(ws, d.ProfileID)
		if d.Trace != nil {
			for i := range d.Trace.Candidates {
				if d.Trace.Candidates[i].ProfileName == "" {
					d.Trace.Candidates[i].ProfileName = veridianProfileName(ws, d.Trace.Candidates[i].Profile)
				}
			}
		}
	}
	level := domain.VeridianDecisionLogTransitions
	if ws != nil {
		level = domain.VeridianNormalizeDecisionLogLevel(ws.Settings.VeridianDecisionLogLevel)
	}
	return list, next, level, nil
}

func (s *veridianQueueExplainService) Recompute(ctx context.Context, req domain.VeridianQueueRecomputeRequest) (int, error) {
	if err := req.Validate(); err != nil {
		return 0, err
	}
	ctx, err := s.authorize(ctx, req.WorkspaceID, true)
	if err != nil {
		return 0, err
	}
	ids, err := s.explainRepo.Recompute(ctx, req.WorkspaceID, req)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to recompute queue")
		return 0, fmt.Errorf("failed to recompute queue: %w", err)
	}
	if len(ids) > 0 {
		// Une ligne de journal resume l'action (jamais une par entree : jusqu'a 5 000).
		detail := fmt.Sprintf("recomputed %d entries (automation=%s node=%s reason=%s profile=%s)",
			len(ids), req.AutomationID, req.NodeID, req.Reason, req.ProfileID)
		if len(detail) > 190 {
			detail = detail[:190]
		}
		if err := s.decisionRepo.Insert(ctx, req.WorkspaceID, &domain.VeridianSendDecision{
			ID: uuid.NewString(), At: time.Now().UTC(), EntryID: ids[0], AutomationID: req.AutomationID, NodeID: req.NodeID,
			Outcome: domain.VeridianOutcomeRecomputed, Reason: req.Reason, Detail: detail,
		}); err != nil {
			s.logger.WithField("error", err.Error()).Warn("recompute: failed to journal the action")
		}
	}
	return len(ids), nil
}
