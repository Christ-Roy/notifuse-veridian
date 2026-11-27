package leader

import (
	"context"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Workspaces : le dépôt des espaces tel que le voit un travail de fond mené. Tant que cet exemplaire ne mène pas, la liste des
// espaces est vide : le worker tourne, ne trouve rien à faire et ne prend aucune entrée. Le reste du dépôt passe tel quel.
// C'est le point de branchement qui évite de toucher aux workers de l'amont (règle 2) : ils parcourent tous List à chaque tour.
type Workspaces struct {
	domain.WorkspaceRepository
	Gate Gate
}

func (w Workspaces) List(ctx context.Context) ([]*domain.Workspace, error) {
	if w.Gate == nil || !w.Gate.Leads() {
		return nil, nil
	}
	return w.WorkspaceRepository.List(ctx)
}

// Follow : enveloppe un dépôt pour un travail de fond (nil reste nil).
func Follow(repo domain.WorkspaceRepository, g Gate) domain.WorkspaceRepository {
	if repo == nil {
		return nil
	}
	return Workspaces{WorkspaceRepository: repo, Gate: g}
}
