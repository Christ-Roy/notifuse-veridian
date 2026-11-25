package boxaccess

import (
	"context"
	"errors"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/standing"
)

// WorkspaceGetter lit un workspace.
type WorkspaceGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Workspace, error)
}

// Standing relit la situation d'une personne dans un espace (standing.Store).
type Standing interface {
	Of(ctx context.Context, workspaceID, userID string) (standing.State, error)
}

// Guard vérifie qu'un accès désigne encore une boîte vivante : le profil existe et porte toujours l'adresse, l'espace est ouvert, et la
// personne qui a créé l'accès y a encore sa place (ni retirée, ni gelée, ni supprimée au Hub). Retirer un profil, son adresse ou cette
// personne coupe ses accès, sans action de plus. Un accès créé par une clé d'API appartient à l'espace, comme la clé. Standing nil : rien
// n'est relu, donc rien ne s'ouvre.
type Guard struct {
	*Store
	WS       WorkspaceGetter
	Standing Standing
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
	if g.Standing == nil {
		return errors.New("accès de boîte : situation non câblée")
	}
	uid, human := a.HumanCreator()
	st, err := g.Standing.Of(ctx, a.WorkspaceID, uid)
	if err != nil {
		return err
	}
	if !st.Space() || (human && !st.Person()) {
		return ErrAuth
	}
	a.ReadOnly = st.Suspended
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
