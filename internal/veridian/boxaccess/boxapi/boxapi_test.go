package boxapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type authz struct {
	res  domain.PermissionResource
	perm domain.PermissionType
	err  error
}

func (a *authz) Authorize(ctx context.Context, _ string, r domain.PermissionResource, p domain.PermissionType) (context.Context, mailapi.Actor, error) {
	a.res, a.perm = r, p
	return ctx, mailapi.Actor{ID: "u1", Kind: "humain"}, a.err
}

type memStore struct {
	created []boxaccess.Access
	err     error
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
