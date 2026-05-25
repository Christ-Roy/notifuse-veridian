package service

// === Veridian patch — Mail provider choice per workspace ===
//
// Service standalone (pas dans le grand VeridianService monolithique) car la
// surface est petite et auto-contenue. Le HTTP handler instantie directement
// ce service. Pattern aligne sur les autres petits services Veridian dedies
// (cf. TransactionalNotificationService, veridian_*_service.go).
//
// Ticket : todo/2026-05-25-mail-send-as-user-via-hub-gateway.md §3.4 (vague 6 2026-05-25).
//
// Voir aussi :
//   - domain/veridian_mail_provider.go : types + interface repo + event
//   - repository/veridian_mail_provider_postgres.go : impl Postgres
//   - migration v48.go : ADD COLUMN workspaces.mail_provider_choice
//   - http/veridian_mail_provider_handler.go : routes GET/POST

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// ErrInvalidMailProviderChoice est retourne par SetMailProviderChoice quand
// la valeur n'est pas dans l'enum autorise. Sentinel pour mapping handler 400.
var ErrInvalidMailProviderChoice = errors.New("invalid mail_provider_choice — must be 'smtp_generic' or 'hub_gmail'")

// VeridianMailProviderService est l'interface du service mail-provider.
// Surface minimale : 1 read + 1 write. Pas de mock generation auto pour
// l'instant (tests handler stubbent via une impl in-memory ou un fake direct).
type VeridianMailProviderService interface {
	// GetMailProviderChoice retourne la preference courante pour un workspace.
	// Proxy direct au repo, semantique de fallback identique (workspace absent
	// → MailProviderSMTPGeneric, pas d'erreur).
	GetMailProviderChoice(ctx context.Context, workspaceID string) (domain.MailProviderChoice, error)

	// SetMailProviderChoice valide + ecrit la preference. Emit un webhook
	// best-effort tenant.mail_provider_choice_changed apres ecriture reussie.
	// Erreurs :
	//   - ErrInvalidMailProviderChoice : choice hors enum (handler => 400)
	//   - repository.ErrWorkspaceNotFoundForMailProvider : workspace inconnu (handler => 404)
	//   - autres : 500 internal_error.
	SetMailProviderChoice(ctx context.Context, workspaceID string, choice domain.MailProviderChoice) (*domain.MailProviderChoiceResponse, error)
}

// veridianMailProviderService implemente VeridianMailProviderService.
type veridianMailProviderService struct {
	repo    domain.VeridianMailProviderRepository
	emitter domain.WebhookEmitter // peut etre nil (mode self-hosted sans Hub)
	logger  logger.Logger
}

// NewVeridianMailProviderService construit le service. L'emitter est optionnel
// (nil = noop, le webhook ne sera juste pas emis).
func NewVeridianMailProviderService(
	repo domain.VeridianMailProviderRepository,
	emitter domain.WebhookEmitter,
	log logger.Logger,
) VeridianMailProviderService {
	return &veridianMailProviderService{
		repo:    repo,
		emitter: emitter,
		logger:  log,
	}
}

// GetMailProviderChoice proxy direct au repo. Pas de webhook (read-only).
func (s *veridianMailProviderService) GetMailProviderChoice(ctx context.Context, workspaceID string) (domain.MailProviderChoice, error) {
	if workspaceID == "" {
		return "", errors.New("workspace_id required")
	}
	return s.repo.GetMailProviderChoice(ctx, workspaceID)
}

// SetMailProviderChoice valide + ecrit + emit webhook.
//
// Flow :
//  1. Valider choice ∈ enum (400 si non).
//  2. Read previous_choice pour audit (best-effort — si la read fail on
//     continue sans previous_choice dans le payload).
//  3. Write via repo (404 si workspace absent).
//  4. Emit webhook tenant.mail_provider_choice_changed (best-effort).
//
// Idempotence : si previous_choice == new_choice, on EMIT QUAND MEME (le repo
// fait l'UPDATE qui est no-op cote DB mais on tient a tracer le re-set cote
// Hub audit). Sinon on perdrait des signaux du genre "user a re-cliqué pour
// confirmer". Pas de cout, le Hub deduplique sur event_id si necessaire.
func (s *veridianMailProviderService) SetMailProviderChoice(ctx context.Context, workspaceID string, choice domain.MailProviderChoice) (*domain.MailProviderChoiceResponse, error) {
	if workspaceID == "" {
		return nil, errors.New("workspace_id required")
	}
	if !domain.IsValidMailProviderChoice(choice) {
		return nil, ErrInvalidMailProviderChoice
	}

	// Read previous_choice pour l'audit webhook. Best-effort : si la read fail
	// (ex: workspace absent → repo retourne default smtp_generic), on continue.
	// On ne plante pas la mutation pour ca.
	previous, prevErr := s.repo.GetMailProviderChoice(ctx, workspaceID)
	if prevErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"error":        prevErr.Error(),
		}).Warn("mail_provider: failed to read previous choice for audit — continuing")
	}

	if err := s.repo.SetMailProviderChoice(ctx, workspaceID, choice); err != nil {
		return nil, fmt.Errorf("set mail_provider_choice: %w", err)
	}

	updatedAt := time.Now().UTC()
	resp := &domain.MailProviderChoiceResponse{
		WorkspaceID: workspaceID,
		Choice:      choice,
		UpdatedAt:   updatedAt.Format(time.RFC3339),
	}

	// Webhook best-effort tenant.mail_provider_choice_changed.
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMailProviderChoiceChanged, workspaceID, map[string]interface{}{
			"workspace_id":    workspaceID,
			"previous_choice": string(previous),
			"new_choice":      string(choice),
			"actor":           "user", // mutation user-side (UI settings)
		})
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id":    workspaceID,
			"previous_choice": string(previous),
			"new_choice":      string(choice),
		}).Info("mail_provider: choice updated")
	}

	return resp, nil
}
