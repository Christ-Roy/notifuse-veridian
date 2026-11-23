package boxaccess

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
)

type wsGetter struct{ ws *domain.Workspace }

func (w wsGetter) GetByID(context.Context, string) (*domain.Workspace, error) {
	if w.ws == nil {
		return nil, errors.New("absent")
	}
	return w.ws, nil
}

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
	ok := &Guard{Store: s, WS: wsGetter{boxWS("jc@boite.fr")}}
	if got, err := ok.Verify(ctx, "jc@boite.fr", secret); err != nil || got.ID != a.ID {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ok.Alive(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	gone := &Guard{Store: s, WS: wsGetter{boxWS("autre@boite.fr")}}
	if _, err := gone.Verify(ctx, "jc@boite.fr", secret); !errors.Is(err, ErrAuth) {
		t.Fatalf("adresse retirée du profil : %v", err)
	}
	if _, err := gone.Alive(ctx, a.ID); !errors.Is(err, ErrAuth) {
		t.Fatalf("session ouverte : %v", err)
	}
	// Une panne de lecture n'est pas un refus d'identifiants.
	if _, err := (&Guard{Store: s, WS: wsGetter{}}).Verify(ctx, "jc@boite.fr", secret); err == nil || errors.Is(err, ErrAuth) {
		t.Fatalf("panne : %v", err)
	}
}
