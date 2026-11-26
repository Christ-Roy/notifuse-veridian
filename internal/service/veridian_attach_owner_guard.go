package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ErrAnotherOwnerExists : attach-owner transfère la propriété ; un autre humain la tient et le transfert n'a pas été demandé.
var ErrAnotherOwnerExists = errors.New("another human owner exists: attach-owner transfers ownership, ask for it with transfer=true")

// ErrRemovedByOwner : cette personne a été administrateur de plateforme de l'espace et son propriétaire l'a retirée (fiche 95).
var ErrRemovedByOwner = errors.New("this person was removed from the workspace by its owner, it is never put back")

// VeridianPersonGone dit si une personne a été retirée d'un espace par son propriétaire. Posée par internal/veridian/platformadmin ;
// absente (application assemblée sans base), personne n'est parti.
var VeridianPersonGone func(ctx context.Context, workspaceID, hubUserID string) (bool, error)

// VeridianPlatformAdmins rend les administrateurs de plateforme d'un espace (identifiants d'ici) : propriétaires qui ne sont pas le client.
var VeridianPlatformAdmins func(ctx context.Context, workspaceID string) (map[string]bool, error)

// ours dit si un membre est à nous (copropriétaire configuré, administrateur de plateforme) : là où « le propriétaire » désigne le client,
// il ne doit jamais être pris pour lui, ni rétrogradé à sa place. Non lu : erreur, on ne devine pas.
func (s *veridianService) ours(ctx context.Context, workspaceID string) (func(*domain.UserWorkspaceWithEmail) bool, error) {
	var admins map[string]bool
	if VeridianPlatformAdmins != nil {
		var err error
		if admins, err = VeridianPlatformAdmins(ctx, workspaceID); err != nil {
			return nil, fmt.Errorf("read platform admins: %w", err)
		}
	}
	return func(m *domain.UserWorkspaceWithEmail) bool { return m != nil && (admins[m.UserID] || s.isDefaultCoOwner(m.Email)) }, nil
}

func refuseIfGone(ctx context.Context, workspaceID, hubUserID string) error {
	if VeridianPersonGone == nil || hubUserID == "" {
		return nil
	}
	gone, err := VeridianPersonGone(ctx, workspaceID, hubUserID)
	if err != nil {
		return fmt.Errorf("read removals: %w", err)
	}
	if gone {
		return ErrRemovedByOwner
	}
	return nil
}

// anotherHumanOwner : un humain autre que `personID` tient la propriété. Le compte root et les nôtres ne sont pas des clients.
func (s *veridianService) anotherHumanOwner(members []*domain.UserWorkspaceWithEmail, personID string, ours func(*domain.UserWorkspaceWithEmail) bool) bool {
	for _, m := range members {
		if m != nil && m.Role == "owner" && m.UserID != personID && m.Type == domain.UserTypeUser &&
			!strings.EqualFold(m.Email, s.rootEmail) && !ours(m) {
			return true
		}
	}
	return false
}
