package domauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox/mailapi"
)

// authz : l'appelant n'est membre que de member ; perm = droit qu'il a (read ou write).
type authz struct {
	member string
	perm   domain.PermissionType
	asked  []domain.PermissionType
}

func (a *authz) Authorize(ctx context.Context, ws string, r domain.PermissionResource, p domain.PermissionType) (context.Context, mailapi.Actor, error) {
	a.asked = append(a.asked, p)
	if ws != a.member {
		return ctx, mailapi.Actor{}, assert.AnError
	}
	if r != domain.PermissionResourceWorkspace || (p == domain.PermissionTypeWrite && a.perm != domain.PermissionTypeWrite) {
		return ctx, mailapi.Actor{}, domain.NewPermissionError(r, p, "x")
	}
	return ctx, mailapi.Actor{ID: "u1", Kind: "humain"}, nil
}

type stubDNS struct{ checks []Check }

func (s *stubDNS) Verify(context.Context, Domain) []Check { return s.checks }

func apiFixture(t *testing.T) (*http.ServeMux, *authz, *stubDNS, *[]string) {
	s, _ := pgStore(t)
	a := &authz{member: "wsa", perm: domain.PermissionTypeWrite}
	dns := &stubDNS{}
	var changed []string
	h := &Handler{Store: s, DNS: dns, Auth: a, Include: inc, RequireAuth: func(n http.Handler) http.Handler { return n },
		Changed: func(ws, d string) { changed = append(changed, ws+"|"+d) }}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, a, dns, &changed
}

func call(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestAPI_ParcoursEtRedaction(t *testing.T) {
	mux, _, dns, changed := apiFixture(t)
	w := call(mux, "POST", "/api/veridian/sendingDomains.create", `{"workspace_id":"wsa","domain":"Client.fr"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created struct{ Domain View }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	d := created.Domain
	assert.Equal(t, "client.fr", d.Domain.Domain)
	require.Len(t, d.Records, 3)
	assert.True(t, strings.HasPrefix(d.Records[0].Value, "v=DKIM1; k=rsa; p=MII"))
	assert.Equal(t, d.Selector+"._domainkey.client.fr", d.Records[0].Name)
	assert.Equal(t, []string{"wsa|client.fr"}, *changed)

	// Règle 9 : aucune réponse ne porte la clé privée, ni claire ni chiffrée.
	for _, body := range []string{w.Body.String(), call(mux, "GET", "/api/veridian/sendingDomains.list?workspace_id=wsa", "").Body.String()} {
		low := strings.ToLower(body)
		assert.NotContains(t, low, "private")
		assert.NotContains(t, low, "encrypted")
		assert.NotContains(t, body, "BEGIN")
	}

	// Vérification : le relevé DNS fixe le statut et vide le cache du signataire.
	dns.checks = []Check{{Kind: KindDKIM, OK: true, Verdict: "pass"}, {Kind: KindSPF, OK: true, Verdict: "pass"}, {Kind: KindDMARC, OK: true, Verdict: "pass"}}
	w = call(mux, "POST", "/api/veridian/sendingDomains.verify", `{"workspace_id":"wsa","id":"`+d.ID+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var verified struct{ Domain View }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &verified))
	assert.Equal(t, StatusVerified, verified.Domain.Status)
	assert.True(t, verified.Domain.Complete)
	assert.NotNil(t, verified.Domain.LastCheckedAt)
	assert.Len(t, *changed, 2)

	// Clé retirée du DNS : retour en attente (plus de signature).
	dns.checks = []Check{{Kind: KindDKIM, Verdict: "missing"}}
	w = call(mux, "POST", "/api/veridian/sendingDomains.verify", `{"workspace_id":"wsa","id":"`+d.ID+`"}`)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &verified))
	assert.Equal(t, StatusPending, verified.Domain.Status)

	w = call(mux, "POST", "/api/veridian/sendingDomains.delete", `{"workspace_id":"wsa","id":"`+d.ID+`"}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, http.StatusNotFound, call(mux, "POST", "/api/veridian/sendingDomains.verify", `{"workspace_id":"wsa","id":"`+d.ID+`"}`).Code)
}

func TestAPI_GardeEspaceEtDroits(t *testing.T) {
	mux, a, _, _ := apiFixture(t)
	w := call(mux, "POST", "/api/veridian/sendingDomains.create", `{"workspace_id":"wsa","domain":"client.fr"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	var created struct{ Domain View }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	id := created.Domain.ID

	// Un espace dont on n'est pas membre : 401, rien n'est lu ni écrit.
	assert.Equal(t, http.StatusUnauthorized, call(mux, "GET", "/api/veridian/sendingDomains.list?workspace_id=wsb", "").Code)
	assert.Equal(t, http.StatusUnauthorized, call(mux, "POST", "/api/veridian/sendingDomains.delete", `{"workspace_id":"wsb","id":"`+id+`"}`).Code)

	// Membre de B avec l'id d'un domaine de A : introuvable, pas supprimé.
	a.member = "wsb"
	assert.Equal(t, http.StatusNotFound, call(mux, "POST", "/api/veridian/sendingDomains.delete", `{"workspace_id":"wsb","id":"`+id+`"}`).Code)
	assert.Equal(t, http.StatusNotFound, call(mux, "POST", "/api/veridian/sendingDomains.verify", `{"workspace_id":"wsb","id":"`+id+`"}`).Code)
	a.member = "wsa"
	assert.Equal(t, http.StatusOK, call(mux, "GET", "/api/veridian/sendingDomains.list?workspace_id=wsa", "").Code)

	// Lecture seule : lister oui, créer, vérifier, supprimer non.
	a.perm = domain.PermissionTypeRead
	a.asked = nil
	assert.Equal(t, http.StatusOK, call(mux, "GET", "/api/veridian/sendingDomains.list?workspace_id=wsa", "").Code)
	assert.Equal(t, http.StatusForbidden, call(mux, "POST", "/api/veridian/sendingDomains.create", `{"workspace_id":"wsa","domain":"autre.fr"}`).Code)
	assert.Equal(t, http.StatusForbidden, call(mux, "POST", "/api/veridian/sendingDomains.verify", `{"workspace_id":"wsa","id":"`+id+`"}`).Code)
	assert.Equal(t, http.StatusForbidden, call(mux, "POST", "/api/veridian/sendingDomains.delete", `{"workspace_id":"wsa","id":"`+id+`"}`).Code)
	assert.Equal(t, []domain.PermissionType{domain.PermissionTypeRead, domain.PermissionTypeWrite, domain.PermissionTypeWrite, domain.PermissionTypeWrite}, a.asked)

	a.perm = domain.PermissionTypeWrite
	assert.Equal(t, http.StatusBadRequest, call(mux, "POST", "/api/veridian/sendingDomains.create", `{"workspace_id":"wsa","domain":"fr"}`).Code)
	assert.Equal(t, http.StatusConflict, call(mux, "POST", "/api/veridian/sendingDomains.create", `{"workspace_id":"wsa","domain":"client.fr"}`).Code)
	assert.Equal(t, http.StatusBadRequest, call(mux, "POST", "/api/veridian/sendingDomains.create", `{"workspace_id":"wsa","domain":"x.fr","private_key":"x"}`).Code)
}
