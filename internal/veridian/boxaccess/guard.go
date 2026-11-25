package boxaccess

import (
	"context"
	"errors"
	"sync/atomic"

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

// Guard vérifie qu'un accès ouvre encore quelque chose : le profil existe et porte toujours l'adresse, l'espace est ouvert, et le titulaire
// de l'accès, s'il en a un, a encore sa place (ni retiré, ni gelé, ni supprimé au Hub). Rien n'est effacé : le titulaire qui revient retrouve
// son accès. Un accès sans titulaire appartient à l'espace. Standing nil : rien n'est relu, donc rien ne s'ouvre.
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
	st, err := g.Standing.Of(ctx, a.WorkspaceID, a.Holder)
	if err != nil {
		return err
	}
	if !st.Space() || (a.Holder != "" && !st.Person()) {
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

// List : Store.List, chaque accès non révoqué dit s'il n'ouvre plus rien.
func (g *Guard) List(ctx context.Context, ws string) ([]Access, error) {
	l, err := g.Store.List(ctx, ws)
	if err != nil {
		return nil, err
	}
	for i := range l {
		if l[i].RevokedAt != nil {
			continue
		}
		if err := g.check(ctx, &l[i]); errors.Is(err, ErrAuth) {
			l[i].Closed = true
		} else if err != nil {
			return nil, err
		}
	}
	return l, nil
}

// CountOpen compte les accès d'un espace qui ouvrent encore quelque chose.
func (g *Guard) CountOpen(ctx context.Context, ws string) (int, error) {
	l, err := g.query(ctx, `WHERE workspace_id = $1 AND revoked_at IS NULL`, ws)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range l {
		switch err := g.check(ctx, &l[i]); {
		case err == nil:
			n++
		case !errors.Is(err, ErrAuth):
			return 0, err
		}
	}
	return n, nil
}

// CutPerson ferme les connexions déjà ouvertes par les accès de cette personne dans l'espace et rend le nombre de ces accès. Appelée APRÈS
// que sa place a été retirée en base : une connexion fermée ne peut plus se rouvrir.
func (g *Guard) CutPerson(ctx context.Context, ws, userID string) (int, error) {
	l, err := g.Held(ctx, ws, userID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(l))
	for _, a := range l {
		ids = append(ids, a.ID)
	}
	CloseSessions(ids...)
	return len(ids), nil
}

type closerBox struct{ f func(ids []string) }

var closer atomic.Value

// SetCloser branche la fermeture des connexions ouvertes (serveur IMAP). Le SMTP relit l'accès à chaque destinataire et à chaque message.
func SetCloser(f func(ids []string)) { closer.Store(closerBox{f}) }

// CloseSessions ferme les connexions ouvertes par ces accès.
func CloseSessions(ids ...string) {
	if b, ok := closer.Load().(closerBox); ok && b.f != nil && len(ids) > 0 {
		b.f(ids)
	}
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
