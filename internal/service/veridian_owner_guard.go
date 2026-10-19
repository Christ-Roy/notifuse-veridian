package service

import "github.com/Notifuse/notifuse/internal/domain"

// veridianRequireOwner refuse toute action de gestion d'équipe à un appelant
// qui n'est pas le owner humain du workspace (fiche 67). Les clés API sont
// des membres de rôle "member" avec FullPermissions : le rôle, pas les
// permissions, tranche. Renvoie *domain.ErrUnauthorized (HTTP 403 via
// classifyAuthError).
func veridianRequireOwner(user *domain.User, uw *domain.UserWorkspace, action string) error {
	if user == nil || uw == nil || uw.Role != "owner" || user.Type == domain.UserTypeAPIKey {
		return &domain.ErrUnauthorized{Message: "only the workspace owner can " + action}
	}
	return nil
}
