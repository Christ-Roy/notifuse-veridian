package domain

import "context"

// Le rôle « admin » d'un espace (VER-263, fiche 95) : tous les droits d'un propriétaire, SAUF supprimer l'espace, transférer la propriété, retirer ou
// modifier un propriétaire. Point de décision UNIQUE : toute règle « réservé au propriétaire » passe par VeridianAdministers (admin compris) ou
// VeridianOwns (propriétaire seul). Posé par la route signée du Hub (internal/veridian/wsadmin), jamais par l'application : Validate continue de
// n'accepter que owner et member, donc aucun ajout local ne peut créer un admin.

const (
	VeridianRoleOwner  = "owner"
	VeridianRoleAdmin  = "admin"
	VeridianRoleMember = "member"
)

// VeridianAdministers : cette personne gère l'espace (propriétaire ou admin).
func (uw *UserWorkspace) VeridianAdministers() bool {
	return uw != nil && (uw.Role == VeridianRoleOwner || uw.Role == VeridianRoleAdmin)
}

// VeridianOwns : cette personne possède l'espace.
func (uw *UserWorkspace) VeridianOwns() bool { return uw != nil && uw.Role == VeridianRoleOwner }

// VeridianAdminLedger : ce que le Hub a désigné, tenu par le dépôt des espaces (table veridian_workspace_admins).
type VeridianAdminLedger interface {
	// VeridianAdminGranted : la désignation est en vigueur (ni révoquée, ni perdue par un retrait).
	VeridianAdminGranted(ctx context.Context, workspaceID, userID string) (bool, error)
	// VeridianAdminWithdrawn : désignée un jour, puis retirée de l'espace, rétrogradée ou révoquée. Rien d'automatique ne la fait revenir.
	VeridianAdminWithdrawn(ctx context.Context, workspaceID, userID string) (bool, error)
}
