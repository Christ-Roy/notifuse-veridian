package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// AutomationService handles automation business logic
type AutomationService struct {
	repo           domain.AutomationRepository
	authService    domain.AuthService
	workspaceRepo  domain.WorkspaceRepository
	emailQueueRepo domain.EmailQueueRepository
	logger         logger.Logger
	secretKey      string // passphrase serveur : chiffre les secrets des nœuds webhook
}

// AutomationLifecycleDependencies enables the atomic production lifecycle.
// It is optional only to preserve narrow unit tests which do not exercise the
// queue; the application wiring always provides it.
type AutomationLifecycleDependencies struct {
	WorkspaceRepo  domain.WorkspaceRepository
	EmailQueueRepo domain.EmailQueueRepository
	// SecretKey est la passphrase serveur (config.Security.SecretKey) qui
	// chiffre au repos les secrets des nœuds webhook (lot 0, 2026-10-10).
	SecretKey string
}

// NewAutomationService creates a new AutomationService
func NewAutomationService(
	repo domain.AutomationRepository,
	authService domain.AuthService,
	logger logger.Logger,
	lifecycle ...AutomationLifecycleDependencies,
) *AutomationService {
	service := &AutomationService{
		repo:        repo,
		authService: authService,
		logger:      logger,
	}
	if len(lifecycle) > 0 {
		service.workspaceRepo = lifecycle[0].WorkspaceRepo
		service.emailQueueRepo = lifecycle[0].EmailQueueRepo
		service.secretKey = lifecycle[0].SecretKey
	}
	return service
}

func (s *AutomationService) beginLifecycleTx(ctx context.Context, workspaceID string) (*sql.Tx, error) {
	if s.workspaceRepo == nil || s.emailQueueRepo == nil {
		return nil, nil
	}
	db, err := s.workspaceRepo.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace database: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin automation lifecycle transaction: %w", err)
	}
	return tx, nil
}

// Create creates a new automation
func (s *AutomationService) Create(ctx context.Context, workspaceID string, automation *domain.Automation) error {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	if err := automation.Validate(); err != nil {
		return fmt.Errorf("invalid automation: %w", err)
	}

	if err := s.prepareWebhookNodes(ctx, workspaceID, automation, false); err != nil {
		return err
	}

	if err := s.repo.Create(ctx, workspaceID, automation); err != nil {
		s.logger.WithField("automation_id", automation.ID).Error(fmt.Sprintf("failed to create automation: %v", err))
		return fmt.Errorf("failed to create automation: %w", err)
	}

	// La réponse de l'API ne doit jamais porter un secret (chiffré ou non).
	domain.RedactWebhookNodeSecretsForAPI(automation.Nodes)

	return nil
}

// prepareWebhookNodes valide l'URL des nœuds webhook (HTTPS, pas d'IP interne
// littérale) et chiffre leur secret avant persistance. Un secret absent
// conserve l'ancien : l'état déjà persisté n'est lu que s'il y a un nœud
// webhook à traiter.
func (s *AutomationService) prepareWebhookNodes(ctx context.Context, workspaceID string, automation *domain.Automation, isUpdate bool) error {
	hasWebhook := false
	for _, n := range automation.Nodes {
		if n == nil || n.Type != domain.NodeTypeWebhook {
			continue
		}
		hasWebhook = true
		if u, ok := n.Config["url"].(string); ok && u != "" {
			if _, err := ValidateTenantOutboundURL(u); err != nil {
				return fmt.Errorf("invalid webhook node %s: %w", n.ID, err)
			}
		}
	}
	if !hasWebhook {
		return nil
	}

	var existing []*domain.AutomationNode
	if isUpdate {
		current, err := s.repo.GetByID(ctx, workspaceID, automation.ID)
		if err != nil {
			return fmt.Errorf("failed to load current automation: %w", err)
		}
		if current != nil {
			existing = current.Nodes
		}
	}
	if err := domain.ApplyWebhookNodeSecretsOnSave(automation.Nodes, existing, s.secretKey); err != nil {
		return fmt.Errorf("invalid automation: %w", err)
	}
	return nil
}

// Get retrieves an automation by ID
func (s *AutomationService) Get(ctx context.Context, workspaceID, automationID string) (*domain.Automation, error) {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeRead) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeRead,
			"Insufficient permissions: read access to automations required",
		)
	}

	automation, err := s.repo.GetByID(ctx, workspaceID, automationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get automation: %w", err)
	}

	domain.RedactWebhookNodeSecretsForAPI(automation.Nodes)

	return automation, nil
}

// List retrieves automations with optional filters
func (s *AutomationService) List(ctx context.Context, workspaceID string, filter domain.AutomationFilter) ([]*domain.Automation, int, error) {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeRead) {
		return nil, 0, domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeRead,
			"Insufficient permissions: read access to automations required",
		)
	}

	automations, count, err := s.repo.List(ctx, workspaceID, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list automations: %w", err)
	}

	for _, a := range automations {
		if a != nil {
			domain.RedactWebhookNodeSecretsForAPI(a.Nodes)
		}
	}

	return automations, count, nil
}

// Update updates an existing automation
func (s *AutomationService) Update(ctx context.Context, workspaceID string, automation *domain.Automation) error {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	if err := automation.Validate(); err != nil {
		return fmt.Errorf("invalid automation: %w", err)
	}

	// If list_id is being removed/empty, check that there are no email nodes in the embedded nodes
	if automation.HasEmailNodeRestriction() {
		if domain.HasEmailNodes(automation.Nodes) {
			return fmt.Errorf("cannot remove list_id from automation with email nodes - remove email nodes first")
		}
	}

	if err := s.prepareWebhookNodes(ctx, workspaceID, automation, true); err != nil {
		return err
	}

	if err := s.repo.Update(ctx, workspaceID, automation); err != nil {
		s.logger.WithField("automation_id", automation.ID).Error(fmt.Sprintf("failed to update automation: %v", err))
		return fmt.Errorf("failed to update automation: %w", err)
	}

	domain.RedactWebhookNodeSecretsForAPI(automation.Nodes)

	return nil
}

// Delete soft-deletes an automation (can delete live automations)
// The repository handles dropping triggers and exiting active contacts
func (s *AutomationService) Delete(ctx context.Context, workspaceID, automationID string) error {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	tx, err := s.beginLifecycleTx(ctx, workspaceID)
	if err != nil {
		return err
	}
	if tx != nil {
		defer tx.Rollback()
		if err := s.repo.DeleteTx(ctx, tx, workspaceID, automationID); err != nil {
			return fmt.Errorf("failed to delete automation: %w", err)
		}
		if _, err := s.emailQueueRepo.DeleteBySourceTx(ctx, tx, domain.EmailQueueSourceAutomation, automationID); err != nil {
			return fmt.Errorf("failed to delete automation queue: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit automation deletion: %w", err)
		}
		return nil
	}

	// Legacy fallback for isolated service tests without lifecycle dependencies.
	// Repository handles:
	// 1. Dropping the DB trigger (if automation was live)
	// 2. Marking all active contact_automations as 'exited'
	// 3. Soft-deleting the automation (setting deleted_at)
	if err := s.repo.Delete(ctx, workspaceID, automationID); err != nil {
		s.logger.WithField("automation_id", automationID).Error(fmt.Sprintf("failed to delete automation: %v", err))
		return fmt.Errorf("failed to delete automation: %w", err)
	}

	return nil
}

// Activate activates an automation (changes status to live and creates trigger)
func (s *AutomationService) Activate(ctx context.Context, workspaceID, automationID string) error {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	tx, err := s.beginLifecycleTx(ctx, workspaceID)
	if err != nil {
		return err
	}
	if tx != nil {
		defer tx.Rollback()
	}

	// Get existing automation, using the lifecycle transaction in production.
	var automation *domain.Automation
	if tx != nil {
		automation, err = s.repo.GetByIDTx(ctx, tx, workspaceID, automationID)
	} else {
		automation, err = s.repo.GetByID(ctx, workspaceID, automationID)
	}
	if err != nil {
		return fmt.Errorf("failed to get automation: %w", err)
	}

	// Check if already live
	if automation.Status == domain.AutomationStatusLive {
		return fmt.Errorf("automation is already live")
	}

	// If no list_id, check that there are no email nodes in the embedded nodes
	if automation.HasEmailNodeRestriction() {
		if domain.HasEmailNodes(automation.Nodes) {
			return fmt.Errorf("cannot activate automation with email nodes when list_id is not set")
		}
	}

	// Update status to live
	automation.Status = domain.AutomationStatusLive
	if tx != nil {
		err = s.repo.UpdateTx(ctx, tx, workspaceID, automation)
	} else {
		err = s.repo.Update(ctx, workspaceID, automation)
	}
	if err != nil {
		return fmt.Errorf("failed to update automation status: %w", err)
	}

	// Create the database trigger
	if tx != nil {
		err = s.repo.CreateAutomationTriggerTx(ctx, tx, workspaceID, automation)
	} else {
		err = s.repo.CreateAutomationTrigger(ctx, workspaceID, automation)
	}
	if err != nil {
		// Rollback status change
		if tx == nil {
			automation.Status = domain.AutomationStatusDraft
			_ = s.repo.Update(ctx, workspaceID, automation)
		}
		return fmt.Errorf("failed to create automation trigger: %w", err)
	}

	if tx != nil {
		if _, err := s.emailQueueRepo.ResumeBySourceTx(ctx, tx, domain.EmailQueueSourceAutomation, automationID); err != nil {
			return fmt.Errorf("failed to resume automation queue: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit automation activation: %w", err)
		}
	}

	return nil
}

// Pause pauses a live automation (changes status to paused and drops trigger)
func (s *AutomationService) Pause(ctx context.Context, workspaceID, automationID string) error {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	tx, err := s.beginLifecycleTx(ctx, workspaceID)
	if err != nil {
		return err
	}
	if tx != nil {
		defer tx.Rollback()
	}

	var automation *domain.Automation
	if tx != nil {
		automation, err = s.repo.GetByIDTx(ctx, tx, workspaceID, automationID)
	} else {
		automation, err = s.repo.GetByID(ctx, workspaceID, automationID)
	}
	if err != nil {
		return fmt.Errorf("failed to get automation: %w", err)
	}

	// Check if live
	if automation.Status != domain.AutomationStatusLive {
		return fmt.Errorf("automation is not live")
	}

	// Drop the database trigger first
	if tx != nil {
		err = s.repo.DropAutomationTriggerTx(ctx, tx, workspaceID, automationID)
	} else {
		err = s.repo.DropAutomationTrigger(ctx, workspaceID, automationID)
	}
	if err != nil {
		return fmt.Errorf("failed to drop automation trigger: %w", err)
	}

	// Update status to paused
	automation.Status = domain.AutomationStatusPaused
	if tx != nil {
		err = s.repo.UpdateTx(ctx, tx, workspaceID, automation)
	} else {
		err = s.repo.Update(ctx, workspaceID, automation)
	}
	if err != nil {
		return fmt.Errorf("failed to update automation status: %w", err)
	}

	if tx != nil {
		if _, err := s.emailQueueRepo.PauseBySourceTx(ctx, tx, domain.EmailQueueSourceAutomation, automationID); err != nil {
			return fmt.Errorf("failed to pause automation queue: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit automation pause: %w", err)
		}
	}

	return nil
}

// GetContactNodeExecutions retrieves the node executions of a contact through an automation
func (s *AutomationService) GetContactNodeExecutions(ctx context.Context, workspaceID, automationID, email string) (*domain.ContactAutomation, []*domain.NodeExecution, error) {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeRead) {
		return nil, nil, domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeRead,
			"Insufficient permissions: read access to automations required",
		)
	}

	// Get the contact automation record
	contactAutomation, err := s.repo.GetContactAutomationByEmail(ctx, workspaceID, automationID, email)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get contact automation: %w", err)
	}

	// Get the node executions
	entries, err := s.repo.GetNodeExecutions(ctx, workspaceID, contactAutomation.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get node executions: %w", err)
	}

	return contactAutomation, entries, nil
}

// resolveContactAutomation finds the ContactAutomation targeted by an
// exit/reset request : directly by contact_automation_id when given, else
// by the (automation_id, email) pair. Shared by ExitContact/ResetContact.
func (s *AutomationService) resolveContactAutomation(ctx context.Context, workspaceID, automationID, email, contactAutomationID string) (*domain.ContactAutomation, error) {
	if contactAutomationID != "" {
		return s.repo.GetContactAutomation(ctx, workspaceID, contactAutomationID)
	}
	if automationID == "" || email == "" {
		return nil, fmt.Errorf("either contact_automation_id, or both automation_id and email, are required")
	}
	return s.repo.GetContactAutomationByEmail(ctx, workspaceID, automationID, email)
}

// ExitContact manually exits ONE contact from an automation, leaving every
// other contact's progress untouched (Veridian 2026-10-03). Idempotent : a
// contact already exited is returned as-is, not an error — exiting twice is
// not a failure. Default reason "manual" (cf domain.ContactAutomation.ExitReason
// doc: completed/filter_rejected/automation_node_deleted/manual/unsubscribed).
func (s *AutomationService) ExitContact(ctx context.Context, workspaceID, automationID, email, contactAutomationID, reason string) (*domain.ContactAutomation, error) {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	ca, err := s.resolveContactAutomation(ctx, workspaceID, automationID, email, contactAutomationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get contact automation: %w", err)
	}

	if ca.Status == domain.ContactAutomationStatusExited {
		return ca, nil // déjà sorti : idempotent, pas une erreur
	}

	if reason == "" {
		reason = "manual"
	}
	ca.Status = domain.ContactAutomationStatusExited
	ca.ExitReason = &reason

	if err := s.repo.UpdateContactAutomation(ctx, workspaceID, ca); err != nil {
		return nil, fmt.Errorf("failed to exit contact from automation: %w", err)
	}

	// Mission 2026-10-04 (audit backend) : ExitContact (sortie MANUELLE, API/
	// console) est le SEUL chemin de sortie qui n'incrementait pas le compteur
	// "exited" -- l'executor (automation_executor.go:214/372) et le service de
	// reply (veridian_reply_service.go:312) le font deja pour la sortie
	// AUTOMATIQUE. Sans cette ligne, un contact sorti a la main par un
	// operateur disparaissait des stats (enrolled != active+completed+exited+
	// failed).
	if incErr := s.repo.IncrementAutomationStat(ctx, workspaceID, ca.AutomationID, "exited"); incErr != nil {
		s.logger.WithField("workspace_id", workspaceID).
			WithField("automation_id", ca.AutomationID).
			WithField("error", incErr.Error()).
			Warn("Failed to increment automation stat \"exited\" after manual ExitContact")
	}
	return ca, nil
}

// ResetContact puts ONE contact back at the automation's start node
// (automation.RootNodeID), status active, scheduled_at=now so the scheduler
// picks it up on its next tick exactly like a fresh enrollment — WITHOUT
// touching node_execution/message_history (Veridian 2026-10-03 : no send or
// skip history is erased, only the ContactAutomation pointer moves back).
func (s *AutomationService) ResetContact(ctx context.Context, workspaceID, automationID, email, contactAutomationID string) (*domain.ContactAutomation, error) {
	ctx, _, userWorkspace, err := s.authService.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	if !userWorkspace.HasPermission(domain.PermissionResourceAutomations, domain.PermissionTypeWrite) {
		return nil, domain.NewPermissionError(
			domain.PermissionResourceAutomations,
			domain.PermissionTypeWrite,
			"Insufficient permissions: write access to automations required",
		)
	}

	ca, err := s.resolveContactAutomation(ctx, workspaceID, automationID, email, contactAutomationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get contact automation: %w", err)
	}

	automation, err := s.repo.GetByID(ctx, workspaceID, ca.AutomationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get automation: %w", err)
	}
	if automation.RootNodeID == "" {
		return nil, fmt.Errorf("automation %s has no root node, cannot reset", automation.ID)
	}

	// Mission 2026-10-04 (audit backend) : capture l'etat TERMINAL quitte AVANT
	// de le muter -- ResetContact doit decrementer "exited"/"completed" pour
	// eviter le double comptage (le contact va re-traverser l'automation et
	// re-incrementer l'un des deux a sa prochaine sortie). "failed" n'est PAS
	// decremente : le fait qu'il ait echoue reste un fait historique vrai,
	// independant du reset.
	previousStatus := ca.Status

	now := time.Now()
	rootNodeID := automation.RootNodeID
	ca.CurrentNodeID = &rootNodeID
	ca.Status = domain.ContactAutomationStatusActive
	ca.ExitReason = nil
	ca.ScheduledAt = &now
	ca.RetryCount = 0
	ca.LastError = nil
	ca.LastRetryAt = nil

	if err := s.repo.UpdateContactAutomation(ctx, workspaceID, ca); err != nil {
		return nil, fmt.Errorf("failed to reset contact: %w", err)
	}

	var statToDecrement string
	switch previousStatus {
	case domain.ContactAutomationStatusExited:
		statToDecrement = "exited"
	case domain.ContactAutomationStatusCompleted:
		statToDecrement = "completed"
	}
	if statToDecrement != "" {
		if decErr := s.repo.DecrementAutomationStat(ctx, workspaceID, ca.AutomationID, statToDecrement); decErr != nil {
			s.logger.WithField("workspace_id", workspaceID).
				WithField("automation_id", ca.AutomationID).
				WithField("error", decErr.Error()).
				Warn("Failed to decrement automation stat after ResetContact")
		}
	}
	return ca, nil
}
