package service

// === Veridian patch — fusible de réputation, API de lecture (2026-09-29) ===
//
// Service du signal "visible dans l'interface ou l'API" exigé par la mission
// qui a introduit le fusible de réputation (internal/service/queue/
// veridian_reputation_gate.go). Recalcule, PAR INTÉGRATION SMTP, EXACTEMENT
// les mêmes requêtes que le gate (via queue.VeridianComputeReputationStatus)
// pour ne jamais diverger de ce qui bloque réellement l'envoi : c'est un
// LECTEUR du même état, pas une seconde source de vérité.
//
// Gardien de sécurité : authentifie l'user pour le workspace demandé et exige
// la permission message_history:read (même contrat que le reply rate / le
// breakdown — cf. veridian_reply_stats_service.go).

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type veridianReputationStatusService struct {
	messageHistoryRepo domain.MessageHistoryRepository
	workspaceRepo      domain.WorkspaceRepository
	authService        domain.AuthService
	logger             logger.Logger
}

// NewVeridianReputationStatusService construit le service de statut réputation.
func NewVeridianReputationStatusService(
	messageHistoryRepo domain.MessageHistoryRepository,
	workspaceRepo domain.WorkspaceRepository,
	authService domain.AuthService,
	log logger.Logger,
) domain.VeridianReputationStatusService {
	return &veridianReputationStatusService{
		messageHistoryRepo: messageHistoryRepo,
		workspaceRepo:      workspaceRepo,
		authService:        authService,
		logger:             log,
	}
}

func (s *veridianReputationStatusService) GetReputationStatus(
	ctx context.Context,
	req *domain.VeridianReputationStatusRequest,
) (*domain.VeridianReputationStatusResponse, error) {
	if req == nil || req.WorkspaceID == "" {
		return nil, fmt.Errorf("workspace_id is required")
	}

	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate user: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceMessageHistory,
			domain.PermissionTypeRead,
			"Insufficient permissions: read access to message history required",
		)
	}

	workspace, err := s.workspaceRepo.GetByID(ctx, req.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load workspace: %w", err)
	}

	resp := &domain.VeridianReputationStatusResponse{
		Integrations: make([]domain.VeridianReputationIntegrationStatus, 0),
	}

	now := time.Now().UTC()
	for _, integration := range workspace.Integrations {
		if integration.EmailProvider.Kind != domain.EmailProviderKindSMTP &&
			integration.EmailProvider.Kind != domain.EmailProviderKindSES &&
			integration.EmailProvider.Kind != domain.EmailProviderKindSparkPost &&
			integration.EmailProvider.Kind != domain.EmailProviderKindPostmark &&
			integration.EmailProvider.Kind != domain.EmailProviderKindMailgun &&
			integration.EmailProvider.Kind != domain.EmailProviderKindMailjet &&
			integration.EmailProvider.Kind != domain.EmailProviderKindSendGrid {
			continue // not an email-sending integration (e.g. imap, llm)
		}
		if len(integration.EmailProvider.Senders) == 0 {
			continue
		}
		senderDomain := queue.VeridianEmailDomain(integration.EmailProvider.Senders[0].Email)
		if senderDomain == "" {
			continue
		}

		status, err := queue.VeridianComputeReputationStatus(ctx, s.messageHistoryRepo, workspace.ID, senderDomain, &integration.EmailProvider, now)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"workspace_id":   workspace.ID,
				"integration_id": integration.ID,
				"error":          err.Error(),
			}).Error("Failed to compute reputation status")
			return nil, fmt.Errorf("failed to compute reputation status: %w", err)
		}

		entry := domain.VeridianReputationIntegrationStatus{
			IntegrationID:   integration.ID,
			IntegrationName: integration.Name,
			SenderDomain:    status.SenderDomain,
			WindowDays:      status.WindowDays,
			Sent7d:          status.Sent7d,
			HardBounces7d:   status.HardBounces7d,
			HardBounceRate:  status.HardBounceRate,
			Threshold:       status.Threshold,
			ThresholdCustom: status.ThresholdCustom,
			MinSent:         status.MinSent,
			Complaints7d:    status.Complaints7d,
			Alert:           status.Alert,
			DomainFactor:    status.DomainFactor,
			Classes:         status.Classes,
			SlowedClasses:   status.SlowedClasses,
			StoppedClasses:  status.StoppedClasses,
		}
		resp.Integrations = append(resp.Integrations, entry)
		if len(entry.StoppedClasses) > 0 {
			resp.AnyStopped = true
		}
		if len(entry.SlowedClasses) > 0 || entry.DomainFactor > 1 {
			resp.AnySlowed = true
		}
	}

	return resp, nil
}
