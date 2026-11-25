package mailapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/standing"
)

type authDouble struct {
	domain.AuthService
	kind domain.UserType
	err  error
}

func (a authDouble) AuthenticateUserForWorkspace(ctx context.Context, _ string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	if a.err != nil {
		return ctx, nil, nil, a.err
	}
	return ctx, &domain.User{ID: "u1", Type: a.kind}, &domain.UserWorkspace{Role: "member", Permissions: domain.FullPermissions}, nil
}

type situation struct {
	st  standing.State
	err error
}

func (f situation) Of(context.Context, string, string) (standing.State, error) { return f.st, f.err }

func TestDomainAuthorizer_FollowsStanding(t *testing.T) {
	const msg, space = domain.PermissionResourceMessageHistory, domain.PermissionResourceWorkspace
	in := standing.State{Workspace: true, Member: true}
	for name, c := range map[string]struct {
		st                        standing.State
		kind                      domain.UserType
		read, write, manageAccess bool
	}{
		"membre":          {in, domain.UserTypeUser, true, true, true},
		"gelé":            {standing.State{Workspace: true, Member: true, Frozen: true}, domain.UserTypeUser, false, false, false},
		"compte supprimé": {standing.State{Workspace: true, Member: true, Deleted: true}, domain.UserTypeUser, false, false, false},
		"espace fermé":    {standing.State{Workspace: true, Member: true, Closed: true}, domain.UserTypeUser, false, false, false},
		"suspendu":        {standing.State{Workspace: true, Member: true, Suspended: true}, domain.UserTypeUser, true, false, true},
		"clé, gel ignoré": {standing.State{Workspace: true, Member: true, Frozen: true}, domain.UserTypeAPIKey, true, true, true},
		"clé, fermé":      {standing.State{Workspace: true, Closed: true}, domain.UserTypeAPIKey, false, false, false},
		"clé, suspendu":   {standing.State{Workspace: true, Suspended: true}, domain.UserTypeAPIKey, true, false, true},
	} {
		d := DomainAuthorizer{Auth: authDouble{kind: c.kind}, Standing: situation{st: c.st}}
		for what, q := range map[string]struct {
			res  domain.PermissionResource
			perm domain.PermissionType
			ok   bool
		}{"lire": {msg, domain.PermissionTypeRead, c.read}, "écrire": {msg, domain.PermissionTypeWrite, c.write}, "gérer les accès": {space, domain.PermissionTypeWrite, c.manageAccess}} {
			_, _, err := d.Authorize(context.Background(), "ws", q.res, q.perm)
			if (err == nil) != q.ok || (err != nil && !denied(err)) {
				t.Errorf("%s/%s : %v", name, what, err)
			}
		}
	}
	for name, d := range map[string]DomainAuthorizer{"panne": {Auth: authDouble{}, Standing: situation{err: errors.New("base")}}, "non câblée": {Auth: authDouble{}}} {
		if _, _, err := d.Authorize(context.Background(), "ws", msg, domain.PermissionTypeRead); err == nil || denied(err) {
			t.Errorf("%s : ni ouverture, ni refus : %v", name, err)
		}
	}
}

// Le flux relit le droit à chaque passage : le message rangé APRÈS le retrait n'arrive jamais, et le flux se ferme.
func TestEvents_ClosedWhenRightIsLost(t *testing.T) {
	for name, refusal := range map[string]error{"retiré": &domain.ErrUnauthorized{Message: "x"}, "session coupée": &domain.ErrAuthenticationFailed{Message: "x"},
		"gelé": domain.NewPermissionError(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead, "x")} {
		t.Run(name, func(t *testing.T) {
			e, _ := newEnv(t)
			auth := e.auth
			seed(t, e)
			resp := openStream(t, e.srv.URL)
			defer resp.Body.Close()
			auth.mu.Lock()
			auth.err = refusal
			auth.mu.Unlock()
			e.ingest(t, 3, replyEML(3), time.Now())
			evs := readEvents(t, resp, 2, 15*time.Second)
			if len(evs) != 1 || evs[0]["event"] != "closed" {
				t.Fatalf("flux fermé sans un message de plus attendu : %v", evs)
			}
		})
	}
}

// Une panne de lecture du droit n'envoie rien et ne ferme pas : au retour, rien n'est perdu.
func TestEvents_RightUnreadableSendsNothing(t *testing.T) {
	e, _ := newEnv(t)
	auth := e.auth
	seed(t, e)
	resp := openStream(t, e.srv.URL)
	defer resp.Body.Close()
	auth.mu.Lock()
	auth.err = errors.New("base injoignable")
	auth.mu.Unlock()
	e.ingest(t, 3, replyEML(3), time.Now())
	got := make(chan []map[string]string, 1)
	go func() { got <- readEvents(t, resp, 1, 20*time.Second) }()
	select {
	case evs := <-got:
		t.Fatalf("rien ne doit partir sans droit lu : %v", evs)
	case <-time.After(3 * time.Second):
	}
	auth.mu.Lock()
	auth.err = nil
	auth.mu.Unlock()
	if evs := <-got; len(evs) != 1 || evs[0]["id"] != "3" {
		t.Fatalf("le message attendait : %v", evs)
	}
}

func openStream(t *testing.T, url string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/veridian/mail.events?workspace_id=ws1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", err, resp)
	}
	return resp
}
