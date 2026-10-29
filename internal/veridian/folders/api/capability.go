package api

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/folders"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func init() {
	platform.Register(platform.Capability{Name: "folders", Wire: wire})
}

// resourceOf : le droit qui garde chaque genre d'objet (les segments sont des objets de contacts).
func resourceOf(kind folders.Kind) (domain.PermissionResource, bool) {
	switch kind {
	case folders.KindSegments:
		return domain.PermissionResourceContacts, true
	case folders.KindLists:
		return domain.PermissionResourceLists, true
	case folders.KindTemplates:
		return domain.PermissionResourceTemplates, true
	}
	return "", false
}

// Authorize applique la regle : appartenance au workspace, puis droit de lecture ou d'ecriture
// du genre. kind "" = appartenance seule.
func Authorize(uw *domain.UserWorkspace, kind folders.Kind, write bool) error {
	if kind == "" {
		return nil
	}
	res, ok := resourceOf(kind)
	if !ok {
		return fmt.Errorf("kind inconnu %q", kind)
	}
	perm := domain.PermissionTypeRead
	if write {
		perm = domain.PermissionTypeWrite
	}
	if uw == nil || !uw.HasPermission(res, perm) {
		return domain.NewPermissionError(res, perm, fmt.Sprintf("Insufficient permissions: %s access to %s required", perm, res))
	}
	return nil
}

func wire(h platform.Host) error {
	auth, repo := h.AuthService(), h.WorkspaceRepository()
	if auth == nil || repo == nil || h.Mux() == nil || h.JWTSecret() == nil {
		// Assemblage de test (App sans services) : rien a cabler. En production ces quatre elements
		// existent toujours ; leur absence se verrait aussitot (routes 404 sur la verification de livraison).
		return nil
	}
	svc := &folders.Service{
		DB: func(ctx context.Context, workspaceID string) (*sql.DB, error) {
			return repo.GetConnection(ctx, workspaceID)
		},
		Authz: func(ctx context.Context, workspaceID string, kind folders.Kind, write bool) error {
			_, _, uw, err := auth.AuthenticateUserForWorkspace(ctx, workspaceID)
			if err != nil {
				return err
			}
			return Authorize(uw, kind, write)
		},
	}
	(&Handler{Svc: svc, GetJWTSecret: h.JWTSecret(), Log: h.Logger()}).RegisterRoutes(h.Mux())
	return nil
}
