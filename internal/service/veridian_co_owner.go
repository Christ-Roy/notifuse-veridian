package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

// === Veridian patch — co-owner par défaut (2026-10-30) ===
//
// Robert Brunon (compte d'administration Veridian) doit être owner de CHAQUE
// espace (workspace) créé, en plus de l'owner client. Le co-owner est désigné
// par la variable d'environnement VERIDIAN_DEFAULT_CO_OWNER_EMAIL (lue dans
// config.Config.VeridianDefaultCoOwnerEmail) ; vide = fonctionnalité
// désactivée, aucun comportement changé.
//
// Aucune migration : user_workspaces n'a pas de contrainte d'unicité sur le
// rôle, et workspaceRepo.AddUserToWorkspace est un UPSERT sur
// (user_id, workspace_id) qui pose le rôle demandé.

// ConfigureDefaultCoOwner active le co-owner par défaut. Injecté après
// construction (même pattern que ConfigureWorkspaceDBCleanup). Une chaîne vide
// désactive la fonctionnalité.
func (s *veridianService) ConfigureDefaultCoOwner(email string) {
	s.defaultCoOwnerEmail = strings.TrimSpace(email)
}

// isDefaultCoOwner indique si email est celui du co-owner configuré
// (comparaison insensible à la casse). Toujours false si désactivé.
func (s *veridianService) isDefaultCoOwner(email string) bool {
	if s.defaultCoOwnerEmail == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(email), s.defaultCoOwnerEmail)
}

// ensureDefaultCoOwner garantit (idempotent) que le co-owner configuré existe
// (type user) et qu'il est rattaché au workspace avec role=owner et les
// permissions complètes. Best-effort : tout échec est journalisé (warn) et
// n'est JAMAIS propagé, pour ne pas faire échouer la création d'un espace
// client.
func (s *veridianService) ensureDefaultCoOwner(ctx context.Context, workspaceID string) {
	if s.defaultCoOwnerEmail == "" {
		return
	}
	warn := func(step string, err error) {
		if s.logger == nil {
			return
		}
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"co_owner":     s.defaultCoOwnerEmail,
			"step":         step,
			"error":        err.Error(),
		}).Warn("veridian: default co-owner not ensured (non-fatal)")
	}

	// 1. Le user existe (type user), sinon on le crée.
	user, err := s.userService.GetUserByEmail(ctx, s.defaultCoOwnerEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			warn("get_user", err)
			return
		}
		user = nil
	}
	if user == nil {
		user = &domain.User{
			ID:        uuid.New().String(),
			Email:     s.defaultCoOwnerEmail,
			Type:      domain.UserTypeUser,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.userRepo.CreateUser(ctx, user); err != nil {
			warn("create_user", err)
			return
		}
	}
	if user.Type != domain.UserTypeUser {
		warn("user_type", errors.New("configured co-owner is not a human user (type="+string(user.Type)+")"))
		return
	}

	// 2. Déjà owner : rien à faire. Une erreur de lecture (« pas membre »,
	// ErrNoRows ou autre) ne bloque pas : l'UPSERT ci-dessous est idempotent.
	now := time.Now().UTC()
	createdAt := now
	if existing, lookupErr := s.workspaceRepo.GetUserWorkspace(ctx, user.ID, workspaceID); lookupErr == nil && existing != nil {
		if existing.Role == "owner" {
			return
		}
		if !existing.CreatedAt.IsZero() {
			createdAt = existing.CreatedAt
		}
	}

	// 3. Rattachement / promotion : UPSERT role=owner, permissions complètes.
	if err := s.workspaceRepo.AddUserToWorkspace(ctx, &domain.UserWorkspace{
		UserID:      user.ID,
		WorkspaceID: workspaceID,
		Role:        "owner",
		Permissions: domain.FullPermissions,
		CreatedAt:   createdAt,
		UpdatedAt:   now,
	}); err != nil {
		warn("attach_owner", err)
		return
	}
	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"co_owner":     s.defaultCoOwnerEmail,
		}).Info("veridian: default co-owner ensured")
	}
}
