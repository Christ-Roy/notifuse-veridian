package boxaccess

import (
	"context"

	"github.com/Notifuse/notifuse/internal/domain"
)

// WorkspaceGetter lit un workspace.
type WorkspaceGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Workspace, error)
}

// Guard vérifie qu'un accès désigne encore une boîte vivante : le profil existe et porte toujours l'adresse. Retirer un
// profil ou son adresse coupe ses accès, sans action de plus.
type Guard struct {
	*Store
	WS WorkspaceGetter
}

// Verify : Store.Verify, puis la boîte existe encore.
func (g *Guard) Verify(ctx context.Context, address, secret string) (*Access, error) {
	a, err := g.Store.Verify(ctx, address, secret)
	if err != nil {
		return nil, err
	}
	if err := g.check(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Alive : Store.Alive, puis la boîte existe encore.
func (g *Guard) Alive(ctx context.Context, id string) (*Access, error) {
	a, err := g.Store.Alive(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := g.check(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (g *Guard) check(ctx context.Context, a *Access) error {
	ws, err := g.WS.GetByID(ctx, a.WorkspaceID)
	if err != nil {
		return err
	}
	if ProfileOf(ws, a.ProfileID, a.Address) == nil {
		return ErrAuth
	}
	return nil
}

// ProfileOf rend le profil d'envoi du workspace s'il porte l'adresse, nil sinon.
func ProfileOf(ws *domain.Workspace, profileID, address string) *domain.Integration {
	if ws == nil {
		return nil
	}
	p := ws.GetIntegrationByID(profileID)
	if p == nil || p.Type != domain.IntegrationTypeEmail {
		return nil
	}
	for _, s := range p.EmailProvider.Senders {
		if NormalizeAddress(s.Email) == NormalizeAddress(address) {
			return p
		}
	}
	return nil
}
