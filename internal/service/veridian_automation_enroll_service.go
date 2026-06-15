package service

// === Veridian patch — sprint AI-first API (2026-06-15) ===
// Service d'enrôlement programmatique de contacts dans une automation.
//
// Réutilise STRICTEMENT la mécanique d'enrôlement existante : la fonction SQL
// automation_enroll_contact (via AutomationRepository.EnrollContact), la même que
// le trigger AFTER INSERT sur contact_timeline appelle. Aucune duplication de
// l'executor ni du cycle de vie ContactAutomation.
//
// Garde-fous métier :
//   - auth : user membre du workspace + permission automations:write (même contrat
//     que AutomationService.Activate/Update — enrôler change l'état des contacts).
//   - l'automation doit être LIVE (un draft/paused n'a pas de trigger actif ; on
//     refuse l'enrôlement manuel dessus pour éviter des contacts orphelins qui ne
//     progresseront pas tant que l'automation n'est pas activée).
//   - idempotence : un contact déjà ACTIF dans l'automation n'est pas ré-enrôlé
//     (no-op explicite "already_active"). Les contacts completed/exited/failed
//     peuvent être ré-enrôlés (nouveau passage dans la cadence).

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianAutomationEnrollService enrolls contacts into an automation by API.
type VeridianAutomationEnrollService struct {
	repo        domain.AutomationRepository
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianAutomationEnrollService builds the enrollment service.
func NewVeridianAutomationEnrollService(
	repo domain.AutomationRepository,
	authService domain.AuthService,
	log logger.Logger,
) *VeridianAutomationEnrollService {
	return &VeridianAutomationEnrollService{
		repo:        repo,
		authService: authService,
		logger:      log,
	}
}

// Enroll authenticates the caller, validates the automation is live, then enrolls
// each (already normalized) contact email, skipping contacts already active.
// It is best-effort per contact: one failing email does not abort the others —
// the per-contact outcome is reported in the response.
func (s *VeridianAutomationEnrollService) Enroll(
	ctx context.Context,
	workspaceID, automationID string,
	emails []string,
) (*domain.VeridianEnrollContactsResponse, error) {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		// Préfixe aligné sur isAuthFailure (handler) → 401 plutôt que 500.
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	automation, err := s.repo.GetByID(ctx, workspaceID, automationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get automation: %w", err)
	}

	if automation.Status != domain.AutomationStatusLive {
		return nil, fmt.Errorf("cannot enroll contacts: automation is not live (status: %s)", automation.Status)
	}
	if automation.RootNodeID == "" {
		return nil, fmt.Errorf("cannot enroll contacts: automation has no root node")
	}

	frequency := domain.TriggerFrequencyEveryTime
	if automation.Trigger != nil && automation.Trigger.Frequency.IsValid() {
		frequency = automation.Trigger.Frequency
	}

	resp := &domain.VeridianEnrollContactsResponse{
		Results: make([]domain.VeridianEnrollResult, 0, len(emails)),
	}

	for _, email := range emails {
		// Idempotence: skip contacts already active in this automation.
		if existing, getErr := s.repo.GetContactAutomationByEmail(ctx, workspaceID, automationID, email); getErr == nil &&
			existing != nil && existing.Status == domain.ContactAutomationStatusActive {
			resp.Skipped++
			resp.Results = append(resp.Results, domain.VeridianEnrollResult{
				Email:  email,
				Status: "already_active",
			})
			continue
		}

		if err := s.repo.EnrollContact(ctx, workspaceID, automationID, automation.RootNodeID, email, frequency); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"workspace_id":  workspaceID,
				"automation_id": automationID,
				"contact_email": email,
				"error":         err.Error(),
			}).Error("failed to enroll contact into automation")
			resp.Failed++
			resp.Results = append(resp.Results, domain.VeridianEnrollResult{
				Email:  email,
				Status: "error",
				Error:  err.Error(),
			})
			continue
		}

		resp.Enrolled++
		resp.Results = append(resp.Results, domain.VeridianEnrollResult{
			Email:  email,
			Status: "enrolled",
		})
	}

	return resp, nil
}
