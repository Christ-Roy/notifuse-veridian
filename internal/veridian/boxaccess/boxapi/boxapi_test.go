package boxapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type authz struct {
	res  domain.PermissionResource
	perm domain.PermissionType
	err  error
	who  *mailapi.Actor
}

func (a *authz) Authorize(ctx context.Context, _ string, r domain.PermissionResource, p domain.PermissionType) (context.Context, mailapi.Actor, error) {
	a.res, a.perm = r, p
	if a.who != nil {
		return ctx, *a.who, a.err
	}
	return ctx, mailapi.Actor{ID: "u1", Kind: "humain"}, a.err
}

type memStore struct {
	created   []boxaccess.Access
	err       error
	one       *boxaccess.Access
	reason    string
	suspended bool
}

func (m *memStore) Create(_ context.Context, a boxaccess.Access) (*boxaccess.Access, string, error) {
	if m.err != nil {
		return nil, "", m.err
	}
	a.ID = "acc1"
	m.created = append(m.created, a)
	return &a, "mot-de-passe-unique", nil
}
func (m *memStore) List(context.Context, string) ([]boxaccess.Access, error) { return m.created, m.err }
func (m *memStore) Revoke(_ context.Context, ws, id string) (*boxaccess.Access, error) {
	if id != "acc1" {
		return nil, boxaccess.ErrNotFound
	}
	return &boxaccess.Access{ID: id, WorkspaceID: ws}, nil
}

func (m *memStore) SetDisabled(_ context.Context, ws, id string, disabled bool) (*boxaccess.Access, error) {
	if id != "acc1" {
		return nil, boxaccess.ErrNotFound
	}
	a := &boxaccess.Access{ID: id, WorkspaceID: ws}
	if disabled {
		now := time.Now()
		a.DisabledAt = &now
	}
	return a, nil
}
func (m *memStore) Get(_ context.Context, ws, id string) (*boxaccess.Access, error) {
	if m.one == nil || m.one.ID != id || ws != "ws" {
		return nil, boxaccess.ErrNotFound
	}
	a := *m.one
	return &a, nil
}
func (m *memStore) Match(_ context.Context, _, address, secret string) (*boxaccess.Access, error) {
	if m.one == nil || secret != "le-bon" || address != m.one.Address {
		return nil, nil
	}
	a := *m.one
	return &a, nil
}
func (m *memStore) Reason(_ context.Context, a *boxaccess.Access) (string, error) {
	a.ReadOnly = m.suspended
	return m.reason, nil
}
func (m *memStore) HolderEmails(context.Context, string) (map[string]string, error) {
	return map[string]string{"u1": "titulaire@t.fr"}, nil
}

type wsGet struct{}

func (wsGet) GetByID(context.Context, string) (*domain.Workspace, error) {
	return &domain.Workspace{ID: "ws", Integrations: domain.Integrations{{ID: "p", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Senders: []domain.EmailSender{{Email: "second@boite.fr"}, {Email: "jc@boite.fr", IsDefault: true}}}}}}, nil
}

func setup() (*http.ServeMux, *authz, *memStore) {
	a, s := &authz{}, &memStore{}
	mux := http.NewServeMux()
	(&Handler{Store: s, Auth: a, WS: wsGet{}, Log: logger.NewLogger(), RequireAuth: func(h http.Handler) http.Handler { return h },
		Connection: Connection{IMAP: &Endpoint{Host: "boite.test", Port: 1993, Security: "tls"}}}).RegisterRoutes(mux)
	return mux, a, s
}

func call(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestRegisterRoutes_CreateListRevoke(t *testing.T) {
	mux, a, s := setup()
	w := call(mux, "POST", "/api/veridian/mail.access.create", `{"workspace_id":"ws","profile_id":"p","label":"Téléphone"}`)
	var out struct {
		Username, Password string
		Connection         Connection
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 201 || out.Username != "jc@boite.fr" || out.Password != "mot-de-passe-unique" || out.Connection.IMAP.Port != 1993 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if a.res != domain.PermissionResourceWorkspace || a.perm != domain.PermissionTypeWrite || !s.created[0].CanSend || s.created[0].CreatedBy != "humain:u1" {
		t.Fatalf("%v %v %+v", a.res, a.perm, s.created[0])
	}
	w = call(mux, "GET", "/api/veridian/mail.access.list?workspace_id=ws", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "mot-de-passe") || a.perm != domain.PermissionTypeRead {
		t.Fatalf("la liste ne rend jamais le mot de passe : %d %s", w.Code, w.Body)
	}
	if w = call(mux, "POST", "/api/veridian/mail.access.revoke", `{"workspace_id":"ws","id":"acc1"}`); w.Code != 200 || a.perm != domain.PermissionTypeWrite {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = call(mux, "POST", "/api/veridian/mail.access.revoke", `{"workspace_id":"ws","id":"autre"}`); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
	if w = call(mux, "POST", "/api/veridian/mail.access.create", `{"workspace_id":"ws","profile_id":"p","can_send":false}`); w.Code != 201 || s.created[1].CanSend {
		t.Fatalf("%d", w.Code)
	}
}

func TestCreate_Refusals(t *testing.T) {
	for name, body := range map[string]string{
		"sans workspace":     `{"profile_id":"p"}`,
		"sans profil":        `{"workspace_id":"ws"}`,
		"profil inconnu":     `{"workspace_id":"ws","profile_id":"zz"}`,
		"adresse d'un autre": `{"workspace_id":"ws","profile_id":"p","address":"x@y.fr"}`,
		"plafond négatif":    `{"workspace_id":"ws","profile_id":"p","daily_cap":-1}`,
		"plafond trop haut":  `{"workspace_id":"ws","profile_id":"p","daily_cap":5000}`,
		"champ inconnu":      `{"workspace_id":"ws","profile_id":"p","password":"choisi"}`,
		"json":               `{`,
	} {
		mux, _, s := setup()
		if w := call(mux, "POST", "/api/veridian/mail.access.create", body); w.Code != 400 || len(s.created) != 0 {
			t.Errorf("%s : %d %s", name, w.Code, w.Body)
		}
	}
	for name, tc := range map[string]struct {
		authErr, storeErr error
		code              int
	}{
		"droit manquant":  {authErr: domain.NewPermissionError(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite, "x"), code: 403},
		"non authentifié": {authErr: errors.New("jeton"), code: 401},
		"trop d'accès":    {storeErr: boxaccess.ErrTooMany, code: 409},
		"panne":           {storeErr: errors.New("base"), code: 500},
	} {
		mux, a, s := setup()
		a.err, s.err = tc.authErr, tc.storeErr
		w := call(mux, "POST", "/api/veridian/mail.access.create", `{"workspace_id":"ws","profile_id":"p"}`)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "base") {
			t.Errorf("%s : %d %s", name, w.Code, w.Body)
		}
	}
}

// Titulaire : une personne crée SON accès ; un accès partagé se crée par le propriétaire ou par une clé d'API (fiche 91).
func TestCreate_Holder(t *testing.T) {
	for name, c := range map[string]struct {
		who    mailapi.Actor
		shared string
		code   int
		holder string
	}{
		"membre":                {mailapi.Actor{ID: "u1", Kind: "humain"}, "false", 201, "u1"},
		"propriétaire":          {mailapi.Actor{ID: "u1", Kind: "humain", Owner: true}, "false", 201, "u1"},
		"propriétaire, partagé": {mailapi.Actor{ID: "u1", Kind: "humain", Owner: true}, "true", 201, ""},
		"membre, partagé":       {mailapi.Actor{ID: "u1", Kind: "humain"}, "true", 403, ""},
		"clé d'API":             {mailapi.Actor{ID: "k1", Kind: "agent"}, "false", 201, ""},
	} {
		mux, a, s := setup()
		a.who = &c.who
		w := call(mux, "POST", "/api/veridian/mail.access.create", `{"workspace_id":"ws","profile_id":"p","shared":`+c.shared+`}`)
		if w.Code != c.code || (c.code == 201) != (len(s.created) == 1) {
			t.Fatalf("%s : %d %s", name, w.Code, w.Body)
		}
		if c.code == 201 && s.created[0].Holder != c.holder {
			t.Errorf("%s : titulaire %q", name, s.created[0].Holder)
		}
	}
}

func TestRevoke_CutsOpenConnections(t *testing.T) {
	var cut []string
	boxaccess.SetCloser(func(ids []string) { cut = append(cut, ids...) })
	t.Cleanup(func() { boxaccess.SetCloser(nil) })
	mux, _, _ := setup()
	call(mux, "POST", "/api/veridian/mail.access.revoke", `{"workspace_id":"ws","id":"autre"}`)
	if len(cut) != 0 {
		t.Fatal("un accès inconnu ne coupe rien")
	}
	call(mux, "POST", "/api/veridian/mail.access.revoke", `{"workspace_id":"ws","id":"acc1"}`)
	if len(cut) != 1 || cut[0] != "acc1" {
		t.Fatal(cut)
	}
}
