package boxaccess

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/standing"
)

type wsGetter struct{ ws *domain.Workspace }

func (w wsGetter) GetByID(context.Context, string) (*domain.Workspace, error) {
	if w.ws == nil {
		return nil, errors.New("absent")
	}
	return w.ws, nil
}

// situation : double de standing.Store, qui note la personne demandée.
type situation struct {
	st   standing.State
	err  error
	user *string
}

func (f situation) Of(_ context.Context, _, user string) (standing.State, error) {
	if f.user != nil {
		*f.user = user
	}
	return f.st, f.err
}

var open = situation{st: standing.State{Workspace: true, Member: true}}

func boxWS(email string) *domain.Workspace {
	return &domain.Workspace{ID: "ws", Integrations: domain.Integrations{
		{ID: "p", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Senders: []domain.EmailSender{{Email: email}}}},
		{ID: "imap", Type: domain.IntegrationTypeIMAP},
	}}
}

func TestProfileOf(t *testing.T) {
	ws := boxWS("JC@boite.fr")
	if ProfileOf(ws, "p", "jc@BOITE.fr") == nil {
		t.Fatal("profil attendu")
	}
	if ProfileOf(ws, "p", "x@boite.fr") != nil || ProfileOf(ws, "imap", "jc@boite.fr") != nil || ProfileOf(ws, "zz", "jc@boite.fr") != nil || ProfileOf(nil, "p", "jc@boite.fr") != nil {
		t.Fatal("aucun profil attendu")
	}
}

func TestGuard_AddressRemovedFromProfileCutsAccess(t *testing.T) {
	s, ctx := NewStore(testDB(t)), context.Background()
	a, secret, err := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "jc@boite.fr", CanSend: true})
	if err != nil {
		t.Fatal(err)
	}
	ok := &Guard{Store: s, WS: wsGetter{boxWS("jc@boite.fr")}, Standing: open}
	if got, err := ok.Verify(ctx, "jc@boite.fr", secret); err != nil || got.ID != a.ID {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ok.Alive(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	gone := &Guard{Store: s, WS: wsGetter{boxWS("autre@boite.fr")}, Standing: open}
	if _, err := gone.Verify(ctx, "jc@boite.fr", secret); !errors.Is(err, ErrAuth) {
		t.Fatalf("adresse retirée du profil : %v", err)
	}
	if _, err := gone.Alive(ctx, a.ID); !errors.Is(err, ErrAuth) {
		t.Fatalf("session ouverte : %v", err)
	}
	// Une panne de lecture n'est pas un refus d'identifiants.
	if _, err := (&Guard{Store: s, WS: wsGetter{}, Standing: open}).Verify(ctx, "jc@boite.fr", secret); err == nil || errors.Is(err, ErrAuth) {
		t.Fatalf("panne : %v", err)
	}
}

func TestGuard_FollowsItsCreator(t *testing.T) {
	s, ctx := NewStore(testDB(t)), context.Background()
	const u = "0a000000-0000-4000-8000-000000000001"
	human, hs, err := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "jc@boite.fr", CanSend: true, CreatedBy: "humain:" + u})
	if err != nil {
		t.Fatal(err)
	}
	key, ks, err := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "jc@boite.fr", CanSend: true, CreatedBy: "agent:" + u})
	if err != nil {
		t.Fatal(err)
	}
	const yes, no = true, false
	for name, c := range map[string]struct {
		st           standing.State
		human, agent bool
		readOnly     bool
	}{
		"membre":          {standing.State{Workspace: true, Member: true}, yes, yes, no},
		"retiré":          {standing.State{Workspace: true}, no, yes, no},
		"gelé":            {standing.State{Workspace: true, Member: true, Frozen: true}, no, yes, no},
		"compte supprimé": {standing.State{Workspace: true, Member: true, Deleted: true}, no, yes, no},
		"espace fermé":    {standing.State{Workspace: true, Member: true, Closed: true}, no, no, no},
		"espace disparu":  {standing.State{}, no, no, no},
		"espace suspendu": {standing.State{Workspace: true, Member: true, Suspended: true}, yes, yes, yes},
	} {
		var asked string
		g := &Guard{Store: s, WS: wsGetter{boxWS("jc@boite.fr")}, Standing: situation{st: c.st, user: &asked}}
		for who, a := range map[string]struct {
			id, secret string
			ok         bool
		}{"humain": {human.ID, hs, c.human}, "agent": {key.ID, ks, c.agent}} {
			v, verr := g.Verify(ctx, "jc@boite.fr", a.secret)
			l, lerr := g.Alive(ctx, a.id)
			if who == "humain" && asked != u {
				t.Errorf("%s : personne demandée %q", name, asked)
			}
			if a.ok {
				if verr != nil || lerr != nil || v.ReadOnly != c.readOnly || l.ReadOnly != c.readOnly {
					t.Errorf("%s/%s : accès attendu : %v %v", name, who, verr, lerr)
				}
			} else if !errors.Is(verr, ErrAuth) || !errors.Is(lerr, ErrAuth) {
				t.Errorf("%s/%s : refus attendu : connexion %v, session ouverte %v", name, who, verr, lerr)
			}
		}
	}
	// Une panne de lecture, ou une garde non câblée, n'ouvre rien et n'est pas un refus d'identifiants.
	for name, g := range map[string]*Guard{"panne": {Store: s, WS: wsGetter{boxWS("jc@boite.fr")}, Standing: situation{err: errors.New("base")}},
		"non câblée": {Store: s, WS: wsGetter{boxWS("jc@boite.fr")}}} {
		if _, err := g.Verify(ctx, "jc@boite.fr", hs); err == nil || errors.Is(err, ErrAuth) {
			t.Errorf("%s : %v", name, err)
		}
	}
}
