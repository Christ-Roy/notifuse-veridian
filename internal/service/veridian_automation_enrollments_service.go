package service

// === Veridian — lecture des inscriptions d'automation (2026-10-27) ===
// Service mince : auth du workspace, permission automations:read (même gardien
// que la file d'envoi), validation du filtre, puis lecture paginée.

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianAutomationEnrollmentService struct {
	reader      domain.VeridianAutomationEnrollmentReader
	authService domain.AuthService
	logger      logger.Logger
}

// NewVeridianAutomationEnrollmentService assemble le service.
func NewVeridianAutomationEnrollmentService(reader domain.VeridianAutomationEnrollmentReader, authService domain.AuthService, log logger.Logger) domain.VeridianAutomationEnrollmentService {
	return &veridianAutomationEnrollmentService{reader: reader, authService: authService, logger: log}
}

// ListEnrollments rend une page d'inscrits. Ordre des contrôles : auth, droit,
// validation, lecture. Un filtre invalide n'atteint jamais la base.
func (s *veridianAutomationEnrollmentService) ListEnrollments(ctx context.Context, f domain.VeridianAutomationEnrollmentFilter) (*domain.VeridianEnrollmentPage, error) {
	ctx, _, membership, err := s.authService.AuthenticateUserForWorkspace(ctx, f.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	if !membership.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(domain.PermissionResourceAutomations, domain.PermissionTypeRead,
			"Insufficient permissions: read access to automations required")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	statuses, err := domain.ParseVeridianEnrollmentStatus(f.Status)
	if err != nil {
		return nil, err
	}
	page, err := s.reader.ListEnrollmentsPage(ctx, f, statuses)
	if err != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to list automation enrollments")
		return nil, fmt.Errorf("failed to list automation enrollments: %w", err)
	}
	return page, nil
}
