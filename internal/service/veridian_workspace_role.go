package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Les décisions de rôle qui demandent plus que le rôle de l'appelant (VER-263, fiche 95). Le prédicat simple est domain.VeridianAdministers.

// ErrAdminWithdrawn : la personne a été admin de cet espace puis en a été retirée ou rétrogradée ; rien d'automatique ne la ramène.
var ErrAdminWithdrawn = errors.New("admin_withdrawn: this person was removed from this workspace; only an explicit act of its owner or of the Hub brings them back")

// ErrOwnerExists : attach-owner rétrograderait un propriétaire humain, sans que le transfert ait été demandé.
var ErrOwnerExists = errors.New("owner_exists: another human owner holds this workspace; ask for a transfer explicitly")

// veridianMayRemoveMember : un propriétaire retire n'importe quel autre membre ; un admin, tout le monde sauf un propriétaire. Cible illisible : refus.
func (s *WorkspaceService) veridianMayRemoveMember(ctx context.Context, requester *domain.UserWorkspace, targetID string) bool {
	if requester.VeridianOwns() {
		return true
	}
	if !requester.VeridianAdministers() {
		return false
	}
	target, err := s.repo.GetUserWorkspace(ctx, targetID, requester.WorkspaceID)
	return err == nil && target != nil && !target.VeridianOwns()
}

// veridianRoleAfterTransfer : l'ancien propriétaire devient membre, ou admin si le Hub l'a désigné (aucune coupure entre les deux rôles).
func (s *WorkspaceService) veridianRoleAfterTransfer(ctx context.Context, former *domain.UserWorkspace) string {
	ledger, ok := s.repo.(domain.VeridianAdminLedger)
	if !ok {
		return domain.VeridianRoleMember
	}
	granted, err := ledger.VeridianAdminGranted(ctx, former.WorkspaceID, former.UserID)
	if err != nil {
		s.logger.WithField("workspace_id", former.WorkspaceID).WithField("error", err.Error()).Error("veridian: admin designation unreadable, former owner becomes member")
		return domain.VeridianRoleMember
	}
	if granted {
		return domain.VeridianRoleAdmin
	}
	return domain.VeridianRoleMember
}

// refuseWithdrawnAdmin : un retrait n'est jamais défait tout seul (synchronisation, poussée d'organisation, rejeu). Registre illisible : on n'ajoute pas.
func (s *veridianService) refuseWithdrawnAdmin(ctx context.Context, workspaceID, userID string) error {
	ledger, ok := s.workspaceRepo.(domain.VeridianAdminLedger)
	if !ok {
		return nil
	}
	withdrawn, err := ledger.VeridianAdminWithdrawn(ctx, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("read admin ledger: %w", err)
	}
	if withdrawn {
		return ErrAdminWithdrawn
	}
	return nil
}

// refuseSilentTransfer : attach-owner sans transfert explicite ne retire jamais la propriété à quelqu'un. Ni le compte racine ni le copropriétaire
// configuré ne comptent : aucun client n'y perd rien. target nil : la personne n'a pas encore de compte ici.
func (s *veridianService) refuseSilentTransfer(ctx context.Context, input domain.AttachOwnerInput, target *domain.User) error {
	if input.Transfer {
		return nil
	}
	members, err := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID)
	if err != nil {
		return fmt.Errorf("list workspace members: %w", err)
	}
	other := false
	for _, m := range members {
		if m == nil || m.Role != domain.VeridianRoleOwner || m.Type == domain.UserTypeAPIKey {
			continue
		}
		if target != nil && m.UserID == target.ID {
			return nil // déjà propriétaire : rien à transférer
		}
		if !s.isDefaultCoOwner(m.Email) && (s.rootEmail == "" || !strings.EqualFold(m.Email, s.rootEmail)) {
			other = true
		}
	}
	if other {
		return ErrOwnerExists
	}
	return nil
}
