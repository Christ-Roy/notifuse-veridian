package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

type fakeStore struct {
	m      *Member
	err    error
	got    string
	gotTen string
}

func (f *fakeStore) Resolve(_ context.Context, hubUserID, tenantID string) (*Member, error) {
	f.got, f.gotTen = hubUserID, tenantID
	return f.m, f.err
}

type fakeDispatch struct {
	status int
	resp   string
	err    error
	method string
	path   string
	query  map[string][]string
	body   []byte
	calls  int
}

func (f *fakeDispatch) Do(_ context.Context, _ *Member, method, path string, query map[string][]string, body []byte) (int, []byte, error) {
	f.calls++
	f.method, f.path, f.query, f.body = method, path, query, body
	return f.status, []byte(f.resp), f.err
}

type rig struct {
	h     *Handler
	store *fakeStore
	disp  *fakeDispatch
	key   hubKey
}

func newRig(t *testing.T, uw *domain.UserWorkspace) *rig {
	t.Helper()
	k := newHubKey(t, "k1")
	srv, _ := jwksServer(t, k)
	st := &fakeStore{m: &Member{UserID: "u1", Email: "u@x.test", Workspace: uw}}
	d := &fakeDispatch{status: 200, resp: `{"contacts":[]}`}
	return &rig{h: &Handler{Verifier: newVerifier(srv), Store: st, Dispatcher: d}, store: st, disp: d, key: k}
}

func (r *rig) post(t *testing.T, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

func (r *rig) rpc(t *testing.T, method, params string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `"`
	if params != "" {
		body += `,"params":` + params
	}
	w := r.post(t, r.key.sign(t, nil), body+`}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

func TestHandler_SansJetonOuJetonRefuse_401(t *testing.T) {
	r := newRig(t, member("owner", nil))
	w := r.post(t, "", `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Get("WWW-Authenticate"), "Bearer")

	bad := r.key.sign(t, func(c *Claims) { c.Audience = []string{"https://autre/api/mcp"} })
	assert.Equal(t, http.StatusUnauthorized, r.post(t, bad, `{"jsonrpc":"2.0","id":1,"method":"ping"}`).Code)
	assert.Equal(t, 0, r.disp.calls)
}

func TestHandler_UtilisateurInconnu_401EtJamaisDeCreation(t *testing.T) {
	r := newRig(t, member("owner", nil))
	r.store.m, r.store.err = nil, ErrUnknownUser
	w := r.post(t, r.key.sign(t, nil), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "hub_user_unknown", "le Hub cree alors le compte puis reessaie")
	assert.Equal(t, "hub-user-1", r.store.got, "retrouve par hub_user_id (sub), pas par email")
}

func TestHandler_TenantIdDuJetonChoisitLEspaceEtNonMembre403(t *testing.T) {
	r := newRig(t, member("owner", nil))
	tok := r.key.sign(t, func(c *Claims) { c.TenantID = "ws-choisi" })
	assert.Equal(t, http.StatusOK, r.post(t, tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`).Code)
	assert.Equal(t, "ws-choisi", r.store.gotTen)

	r.store.m, r.store.err = nil, ErrNotMember
	w := r.post(t, tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "tenant_forbidden")
	assert.Contains(t, w.Body.String(), `"error":"tenant_forbidden"`, "aussi dans error.data.error")

	r.store.err = ErrUnknownTenant
	w = r.post(t, tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	assert.Equal(t, http.StatusForbidden, w.Code, "jamais de 404 ni de 401 ni de repli silencieux")
	assert.Contains(t, w.Body.String(), "tenant_not_linked")
	assert.Contains(t, w.Body.String(), `"error":"tenant_not_linked"`)
}

func TestHandler_SansEspace_403(t *testing.T) {
	r := newRig(t, nil)
	r.store.m, r.store.err = nil, ErrNoWorkspace
	assert.Equal(t, http.StatusForbidden, r.post(t, r.key.sign(t, nil), `{"jsonrpc":"2.0","id":1,"method":"ping"}`).Code)
	r.store.err = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, r.post(t, r.key.sign(t, nil), `{"jsonrpc":"2.0","id":1,"method":"ping"}`).Code)
}

func TestHandler_MethodeEtCorpsInvalides(t *testing.T) {
	r := newRig(t, member("owner", nil))
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/mcp", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	tok := r.key.sign(t, nil)
	assert.Equal(t, http.StatusBadRequest, r.post(t, tok, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`).Code, "pas de lot")
	assert.Equal(t, http.StatusBadRequest, r.post(t, tok, `{"jsonrpc":"1.0","id":1,"method":"ping"}`).Code)
	assert.Equal(t, http.StatusRequestEntityTooLarge, r.post(t, tok, strings.Repeat("a", maxRequestBytes+10)).Code)
	assert.Equal(t, http.StatusAccepted, r.post(t, tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`).Code, "notification : pas de reponse")
}

func TestHandler_InitializeEtPing(t *testing.T) {
	r := newRig(t, member("owner", nil))
	res := r.rpc(t, "initialize", `{"protocolVersion":"2025-03-26"}`)["result"].(map[string]any)
	assert.Equal(t, "2025-03-26", res["protocolVersion"])
	assert.Contains(t, res["instructions"], "ws1")
	assert.Equal(t, "2025-06-18", r.rpc(t, "initialize", `{"protocolVersion":"1999-01-01"}`)["result"].(map[string]any)["protocolVersion"])
	assert.NotNil(t, r.rpc(t, "ping", "")["result"])
	assert.Equal(t, float64(-32601), r.rpc(t, "inconnue", "")["error"].(map[string]any)["code"])
}

func namesOf(t *testing.T, out map[string]any) []string {
	t.Helper()
	var names []string
	for _, x := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

func TestHandler_ToolsListFiltreeParLesDroits(t *testing.T) {
	owner := newRig(t, member("owner", nil))
	assert.Len(t, namesOf(t, owner.rpc(t, "tools/list", "")), len(catalog))

	ro := newRig(t, member("member", domain.UserPermissions{domain.PermissionResourceContacts: {Read: true}}))
	names := namesOf(t, ro.rpc(t, "tools/list", ""))
	assert.ElementsMatch(t, []string{"contacts_list", "contacts_get_by_email", "segments_list"}, names)
	assert.NotContains(t, names, "transactional_send")

	none := newRig(t, member("member", nil))
	assert.Empty(t, namesOf(t, none.rpc(t, "tools/list", "")))
}

func TestHandler_ToolsCall_RejoueSurLEspaceDeLUtilisateur(t *testing.T) {
	r := newRig(t, member("owner", nil))
	out := r.rpc(t, "tools/call", `{"name":"contacts_get_by_email","arguments":{"email":"a@b.test","workspace_id":"AUTRE"}}`)
	res := out["result"].(map[string]any)
	assert.Equal(t, false, res["isError"])
	assert.Equal(t, http.MethodGet, r.disp.method)
	assert.Equal(t, "/api/contacts.getByEmail", r.disp.path)
	assert.Equal(t, []string{"ws1"}, r.disp.query["workspace_id"])
}

func TestHandler_ToolsCall_DroitInsuffisantNAppellePasLaRoute(t *testing.T) {
	r := newRig(t, member("member", domain.UserPermissions{domain.PermissionResourceTransactional: {Read: true}}))
	out := r.rpc(t, "tools/call", `{"name":"transactional_send","arguments":{"notification":{"id":"x"}}}`)
	assert.Equal(t, true, out["result"].(map[string]any)["isError"])
	assert.Equal(t, 0, r.disp.calls, "aucun envoi")
}

func TestHandler_ToolsCall_ErreursDeLUpstreamEtArgumentsInvalides(t *testing.T) {
	r := newRig(t, member("owner", nil))
	r.disp.status, r.disp.resp = http.StatusForbidden, `{"error":"no"}`
	assert.Equal(t, true, r.rpc(t, "tools/call", `{"name":"lists_list"}`)["result"].(map[string]any)["isError"])

	r.disp.status, r.disp.err = 200, errors.New("boom")
	assert.Equal(t, true, r.rpc(t, "tools/call", `{"name":"lists_list"}`)["result"].(map[string]any)["isError"])

	assert.Equal(t, true, r.rpc(t, "tools/call", `{"name":"templates_get","arguments":{}}`)["result"].(map[string]any)["isError"], "argument requis")
	assert.NotNil(t, r.rpc(t, "tools/call", `{"name":"nexiste_pas"}`)["error"])
	assert.NotNil(t, r.rpc(t, "tools/call", `{}`)["error"])
}

func TestToolResult_TronqueLesGrosseReponses(t *testing.T) {
	out := toolResult(json.RawMessage("1"), strings.Repeat("x", maxResultChars+500), false)
	text := out["result"].(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
	assert.Contains(t, text, "tronque")
	assert.Less(t, len(text), maxResultChars+200)
}
