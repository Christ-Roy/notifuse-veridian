package actions

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mcp"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	testIss = "https://hub.test"
	testAud = "https://notifuse.test/api/veridian/actions"
	hubUser = "11111111-2222-3333-4444-555555555555"
	tenant  = "tenant-1"
	body    = `{"list_ids":["l1"],"contacts":[{"email":"a@x.test","siren":"123456789"}]}`
)

// Hub de test : clé ES256 locale, JWKS servi en HTTP.
type testHub struct {
	key *ecdsa.PrivateKey
	url string
}

func newHub(t *testing.T) *testHub {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pad := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(append(make([]byte, 32-len(b)), b...))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "EC", "crv": "P-256", "kid": "k1", "x": pad(k.X.Bytes()), "y": pad(k.Y.Bytes())}}})
	}))
	t.Cleanup(srv.Close)
	return &testHub{key: k, url: srv.URL}
}

var seq int

func (h *testHub) token(t *testing.T, mutate func(*mcp.Claims)) string {
	t.Helper()
	seq++
	now := time.Now()
	c := &mcp.Claims{Email: "u@x.test", EmailVerified: true, Scope: "crm:tache.ecrire notifuse:contacts.ecrire", TenantID: tenant,
		Act: &mcp.Actor{Subject: "app:prospection"},
		RegisteredClaims: jwt.RegisteredClaims{Issuer: testIss, Audience: jwt.ClaimStrings{testAud}, Subject: hubUser, ID: fmt.Sprintf("jeton-%06d", seq),
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(119 * time.Second))}}
	if mutate != nil {
		mutate(c)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, c)
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(h.key)
	require.NoError(t, err)
	return s
}

type fakeStore struct {
	seen       map[string]bool
	done       map[string]json.RawMessage
	resolveErr error
	beginErr   error
	jtiErr     error
	resolved   []string
	origins    []Origin
	released   int

	enrolled    int
	enrolledErr error
	counted     []string
}

func (f *fakeStore) ConsumeJTI(_ context.Context, jti string) (bool, error) {
	if f.jtiErr != nil {
		return false, f.jtiErr
	}
	if f.seen[jti] {
		return false, nil
	}
	f.seen[jti] = true
	return true, nil
}
func (f *fakeStore) Resolve(_ context.Context, u, tn string) (*mcp.Member, error) {
	f.resolved = append(f.resolved, u+"@"+tn)
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return &mcp.Member{UserID: "u1", Workspace: &domain.UserWorkspace{WorkspaceID: "ws1"}}, nil
}
func (f *fakeStore) Begin(_ context.Context, ws, key, _, _ string, o Origin) (json.RawMessage, error) {
	f.origins = append(f.origins, o)
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.done[ws+o.App+key], nil
}
func (f *fakeStore) Finish(_ context.Context, ws, app, key string, r json.RawMessage) error {
	f.done[ws+app+key] = r
	return nil
}
func (f *fakeStore) EnrolledToday(_ context.Context, ws, app, key, u string) (int, error) {
	f.counted = append(f.counted, ws+"|"+app+"|"+key+"|"+u)
	return f.enrolled, f.enrolledErr
}
func (f *fakeStore) Release(context.Context, string, string, string) error { f.released++; return nil }

type fakeDispatch struct {
	status int
	out    string
	err    error
	bodies []string
	paths  []string

	queries []map[string][]string
	replies []reply // si posées, une réponse par requête
}

type reply struct {
	status int
	out    string
}

func (f *fakeDispatch) Do(_ context.Context, _ *mcp.Member, method, path string, q map[string][]string, b []byte) (int, []byte, error) {
	f.queries = append(f.queries, q)
	f.paths = append(f.paths, method+" "+path)
	f.bodies = append(f.bodies, string(b))
	if len(f.replies) > 0 {
		r := f.replies[0]
		f.replies = f.replies[1:]
		return r.status, []byte(r.out), nil
	}
	return f.status, []byte(f.out), f.err
}

type env struct {
	hub *testHub
	st  *fakeStore
	d   *fakeDispatch
	mux *http.ServeMux
}

func newEnv(t *testing.T) *env {
	e := &env{hub: newHub(t), st: &fakeStore{seen: map[string]bool{}, done: map[string]json.RawMessage{}}, d: &fakeDispatch{status: 200, out: `{"created":1}`}, mux: http.NewServeMux()}
	Mount(e.mux, &Handler{Verifier: &mcp.Verifier{JWKSURL: e.hub.url, Issuer: testIss, Audience: testAud}, Store: e.st, Dispatcher: e.d, Log: logger.NewLogger()})
	return e
}

func (e *env) post(action, token, key, b string) (int, map[string]any) {
	r := httptest.NewRequest("POST", "/api/veridian/actions/"+action, strings.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

const imp = "contacts.importer"

func TestJeton_Refuses(t *testing.T) {
	autre := newHub(t)
	for name, tc := range map[string]struct {
		token  func(e *env) string
		status int
		code   string
	}{
		"aucun jeton":        {func(*env) string { return "" }, 401, "jeton_refuse"},
		"signé par un autre": {func(*env) string { return autre.token(t, nil) }, 401, "jeton_refuse"},
		"autre émetteur":     {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.Issuer = "https://x.test" }) }, 401, "jeton_refuse"},
		"jeton MCP": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) { c.Audience = jwt.ClaimStrings{"https://notifuse.test/api/mcp"} })
		}, 401, "jeton_refuse"},
		"expiré": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) {
				c.IssuedAt = jwt.NewNumericDate(time.Now().Add(-5 * time.Minute))
				c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
			})
		}, 401, "jeton_refuse"},
		"sans act":     {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.Act = nil }) }, 401, "jeton_refuse"},
		"act sans app": {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.Act.Subject = "prospection" }) }, 401, "jeton_refuse"},
		"délégation en chaîne": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) { c.Act.Act = &mcp.Actor{Subject: "app:crm"} })
		}, 401, "jeton_refuse"},
		"sans tenant": {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.TenantID = "" }) }, 401, "jeton_refuse"},
		"sans jti":    {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.ID = "" }) }, 401, "jeton_refuse"},
		"sans iat":    {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.IssuedAt = nil }) }, 401, "jeton_refuse"},
		"vit trop longtemps": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(5 * time.Minute)) })
		}, 401, "jeton_refuse"},
		"iat menteur": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) {
				c.IssuedAt = jwt.NewNumericDate(time.Now().Add(4 * time.Minute))
				c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(5 * time.Minute))
			})
		}, 401, "jeton_refuse"},
		"jeton long presque fini": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) {
				c.IssuedAt = jwt.NewNumericDate(time.Now().Add(-9 * time.Minute))
				c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Minute))
			})
		}, 401, "jeton_refuse"},
		"email non prouvé": {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.EmailVerified = false }) }, 401, "jeton_refuse"},
		"portée absente":   {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.Scope = "crm:tache.ecrire" }) }, 403, "portee_insuffisante"},
		"portée en préfixe": {func(e *env) string {
			return e.hub.token(t, func(c *mcp.Claims) { c.Scope = "notifuse:contacts.ecrire.tout" })
		}, 403, "portee_insuffisante"},
		"app inconnue": {func(e *env) string { return e.hub.token(t, func(c *mcp.Claims) { c.Act.Subject = "app:rybbit" }) }, 403, "app_non_autorisee"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			st, out := e.post(imp, tc.token(e), "cle-000001", body)
			assert.Equal(t, tc.status, st, out)
			assert.Equal(t, tc.code, out["error"])
			assert.Empty(t, e.d.paths, "rien n'est exécuté")
			assert.Empty(t, e.st.resolved, "aucun espace n'est résolu")
		})
	}
}

func TestJeton_AlgorithmeNone_EtHS256_Refuses(t *testing.T) {
	e := newEnv(t)
	c := &mcp.Claims{EmailVerified: true, TenantID: tenant, Act: &mcp.Actor{Subject: "app:crm"}, RegisteredClaims: jwt.RegisteredClaims{Issuer: testIss, Audience: jwt.ClaimStrings{testAud}, Subject: hubUser, ID: "jeton-none",
		IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, c)
	none.Header["kid"] = "k1"
	s, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	hs.Header["kid"] = "k1"
	s2, err := hs.SignedString([]byte("secret"))
	require.NoError(t, err)
	for _, tok := range []string{s, s2} {
		st, _ := e.post(imp, tok, "cle-000001", body)
		assert.Equal(t, 401, st)
	}
	assert.Empty(t, e.d.paths)
}

func TestJeton_NeSertQuUneFois(t *testing.T) {
	e := newEnv(t)
	tok := e.hub.token(t, nil)
	st, _ := e.post(imp, tok, "cle-000001", body)
	require.Equal(t, 200, st)
	st, out := e.post(imp, tok, "cle-000002", body)
	assert.Equal(t, 401, st)
	assert.Equal(t, "jeton_deja_vu", out["error"])
	assert.Len(t, e.d.paths, 1)

	// Consommé même quand la demande est refusée ensuite.
	tok = e.hub.token(t, nil)
	st, _ = e.post("inconnue", tok, "cle-000003", body)
	assert.Equal(t, 404, st)
	st, out = e.post(imp, tok, "cle-000003", body)
	assert.Equal(t, 401, st)
	assert.Equal(t, "jeton_deja_vu", out["error"])

	// Mémoire des jetons injoignable : refus, jamais d'exécution à l'aveugle.
	e.st.jtiErr = errors.New("base coupée")
	st, _ = e.post(imp, e.hub.token(t, nil), "cle-000004", body)
	assert.Equal(t, 503, st)
	assert.Len(t, e.d.paths, 1)
}

func TestEspace_VientDuJeton_JamaisDuCorps(t *testing.T) {
	e := newEnv(t)
	st, _ := e.post(imp, e.hub.token(t, nil), "cle-000001", body)
	require.Equal(t, 200, st)
	assert.Equal(t, []string{hubUser + "@" + tenant}, e.st.resolved)
	assert.Contains(t, e.d.bodies[0], `"workspace_id":"ws1"`)
	st, out := e.post(imp, e.hub.token(t, nil), "cle-000002", `{"workspace_id":"autre","list_ids":["l1"],"contacts":[{"email":"a@x.test"}]}`)
	assert.Equal(t, 400, st, out)
	assert.Len(t, e.d.paths, 1)
	for err, want := range map[error]int{ErrUnlinkedTenant: 409, ErrUnknownUser: 403, ErrNotMember: 403, ErrSuspended: 403, errors.New("x"): 503} {
		e.st.resolveErr = err
		st, _ := e.post(imp, e.hub.token(t, nil), "cle-000009", body)
		assert.Equal(t, want, st, err)
	}
	assert.Len(t, e.d.paths, 1)
}

// « Envoyer vers Mail » ne peut rien faire partir, ni toucher une fiche existante, ni mentir sur sa source.
func TestImport_AucunEnvoi_SourcePosee(t *testing.T) {
	e := newEnv(t)
	st, out := e.post(imp, e.hub.token(t, func(c *mcp.Claims) { c.Act.Subject = "app:crm" }), "cle-000001",
		`{"list_ids":["l1"],"contacts":[{"email":"a@x.test","veridian_source":"mensonge"}]}`)
	require.Equal(t, 200, st, out)
	assert.Equal(t, []string{"POST /api/veridian/contacts.intake"}, e.d.paths)
	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(e.d.bodies[0]), &sent))
	assert.Equal(t, false, sent["allow_triggers"])
	assert.Equal(t, "attach", sent["existing"])
	assert.Equal(t, "crm", sent["contacts"].([]any)[0].(map[string]any)["veridian_source"])
	assert.Equal(t, false, out["rejoue"])
	assert.Equal(t, []Origin{{App: "app:crm", JTI: e.st.origins[0].JTI, HubUserID: hubUser}}, e.st.origins)

	for _, bad := range []string{`{}`, `[]`, `{"list_ids":[],"contacts":[{"email":"a@x.test"}]}`, `{"list_ids":["l1"],"contacts":[]}`, `{"list_ids":["l1"],"contacts":[null]}`,
		`{"list_ids":["l1"],"contacts":[{"email":"a@x.test"}],"allow_triggers":true}`, `{"list_ids":["l1"],"contacts":[{"email":"a@x.test"}],"automation_id":"z"}`,
		body + body, `{"list_ids":["l1"],"contacts":[` + strings.Repeat(`{"email":"a@x.test"},`, 1000) + `{"email":"b@x.test"}]}`} {
		st, _ := e.post(imp, e.hub.token(t, nil), "cle-000002", bad)
		assert.Equal(t, 400, st, bad[:min(60, len(bad))])
	}
	st, _ = e.post(imp, e.hub.token(t, nil), "cle-000002", `{"list_ids":["`+strings.Repeat("x", maxBody)+`"]}`)
	assert.Equal(t, 413, st)
	assert.Len(t, e.d.paths, 1)
}

func TestPortee_UnSeulNom(t *testing.T) {
	e := newEnv(t)
	for _, scope := range []string{"notifuse:contacts.importer", "veridian", "notifuse:*", ""} {
		st, _ := e.post(imp, e.hub.token(t, func(c *mcp.Claims) { c.Scope = scope }), "cle-000009", body)
		assert.Equal(t, 403, st, scope)
	}
	assert.Empty(t, e.d.paths)
}

func TestIdempotence(t *testing.T) {
	e := newEnv(t)
	st, out := e.post(imp, e.hub.token(t, nil), "", body)
	assert.Equal(t, 400, st)
	assert.Equal(t, "cle_requise", out["error"])

	st, _ = e.post(imp, e.hub.token(t, nil), "cle-000001", body)
	require.Equal(t, 200, st)
	st, out = e.post(imp, e.hub.token(t, nil), "cle-000001", body)
	assert.Equal(t, 200, st)
	assert.Equal(t, true, out["rejoue"])
	assert.Equal(t, map[string]any{"created": float64(1)}, out["resultat"], "le premier résultat")
	assert.Len(t, e.d.paths, 1, "un rejeu n'exécute rien")

	e.st.beginErr = ErrInProgress
	st, _ = e.post(imp, e.hub.token(t, nil), "cle-000002", body)
	assert.Equal(t, 409, st)
	e.st.beginErr = ErrKeyReused
	st, _ = e.post(imp, e.hub.token(t, nil), "cle-000002", body)
	assert.Equal(t, 422, st)
	assert.Len(t, e.d.paths, 1)
	e.st.beginErr = nil

	// Simulation : ni clé, ni résultat gardé.
	st, out = e.post(imp, e.hub.token(t, nil), "", `{"dry_run":true,"list_ids":["l1"],"contacts":[{"email":"a@x.test"}]}`)
	assert.Equal(t, 200, st)
	assert.Equal(t, true, out["simulation"])
	assert.Len(t, e.st.done, 1)

	// Un refus ou une panne rend la clé.
	e.d.status, e.d.out = 409, `{"error":"cet import démarrerait une séquence","code":"would_trigger"}`
	st, out = e.post(imp, e.hub.token(t, nil), "cle-000003", body)
	assert.Equal(t, 409, st)
	assert.Equal(t, "cet import démarrerait une séquence", out["reason"])
	for _, bad := range []struct {
		s int
		o string
	}{{500, `{}`}, {200, ``}, {200, `pas du json`}} {
		e.d.status, e.d.out = bad.s, bad.o
		st, _ = e.post(imp, e.hub.token(t, nil), "cle-000003", body)
		assert.Equal(t, 503, st, bad)
	}
	assert.Equal(t, 4, e.st.released)
	assert.Len(t, e.st.done, 1)
}

func TestCatalogue_AucuneActionNEnvoie(t *testing.T) {
	want := map[string]string{"contacts.importer": "notifuse:contacts.ecrire", "listes.lister": "notifuse:contacts.ecrire", "listes.creer": "notifuse:contacts.ecrire",
		"contacts.lire": "notifuse:contacts.lire", "sequences.lister": "notifuse:contacts.ecrire", "sequences.inscrire": "notifuse:sequences.inscrire"}
	got := map[string]string{}
	for n, a := range catalogue {
		got[n] = a.scope
		assert.Equal(t, []string{"app:prospection", "app:crm"}, a.apps)
	}
	assert.Equal(t, want, got, "une action neuve se relit ici, avec sa portée du registre du Hub")
	// Aucune action n'envoie : la seule qui fasse partir du courrier passe par reservoir.activate, dont les portes d'envoi décident ensuite.
	allowed := map[string][]string{
		"contacts.importer":  {"POST /api/veridian/contacts.intake"},
		"listes.lister":      {"GET /api/veridian/reservoir.lists"},
		"listes.creer":       {"GET /api/lists.get", "POST /api/lists.create"},
		"contacts.lire":      {"POST /api/veridian/contacts.state"},
		"sequences.lister":   {"GET /api/automations.list"},
		"sequences.inscrire": {"POST /api/veridian/contacts.intake", "POST /api/veridian/reservoir.activate"},
	}
	for n, b := range map[string]string{"contacts.importer": body, "listes.lister": "", "listes.creer": `{"id":"l9","name":"Lot"}`, "contacts.lire": `{"emails":["a@x.test"]}`, "sequences.lister": "", "sequences.inscrire": enrollBody} {
		e := newEnv(t)
		e.post(n, e.hub.token(t, func(c *mcp.Claims) { c.Scope += " notifuse:sequences.inscrire notifuse:contacts.lire" }), "cle-"+n, b)
		require.NotEmpty(t, e.d.paths, n)
		for _, p := range e.d.paths {
			assert.Contains(t, allowed[n], p, n)
		}
	}
}

func TestListes_Lister(t *testing.T) {
	e := newEnv(t)
	e.d.out = `{"lists":[{"id":"l1","name":"Réserve","starts_sequence":false}]}`
	for _, b := range []string{"", "{}"} {
		st, out := e.post("listes.lister", e.hub.token(t, nil), "", b)
		require.Equal(t, 200, st, out)
		assert.Len(t, out["resultat"].(map[string]any)["lists"], 1)
	}
	assert.Equal(t, []string{"GET /api/veridian/reservoir.lists", "GET /api/veridian/reservoir.lists"}, e.d.paths)
	assert.Equal(t, []string{"ws1"}, e.d.queries[0]["workspace_id"], "l'espace du jeton")
	assert.Empty(t, e.st.origins, "une lecture ne réserve aucune clé")
	st, _ := e.post("listes.lister", e.hub.token(t, nil), "", `{"workspace_id":"autre"}`)
	assert.Equal(t, 400, st)
}

func TestListes_Creer(t *testing.T) {
	e := newEnv(t)
	step := func(key string, replies ...reply) (int, map[string]any) {
		e.d.replies, e.d.paths, e.d.bodies = replies, nil, nil
		return e.post("listes.creer", e.hub.token(t, nil), key, `{"id":"lotnovembre","name":" Lot de novembre "}`)
	}
	st, out := step("cle-000001", reply{404, `{"error":"List not found"}`}, reply{201, `{"list":{"id":"lotnovembre"}}`})
	require.Equal(t, 200, st, out)
	assert.Equal(t, map[string]any{"id": "lotnovembre", "name": "Lot de novembre", "existait": false}, out["resultat"])
	assert.Equal(t, []string{"GET /api/lists.get", "POST /api/lists.create"}, e.d.paths)
	assert.JSONEq(t, `{"workspace_id":"ws1","id":"lotnovembre","name":"Lot de novembre","is_public":false,"is_double_optin":false}`, e.d.bodies[1])

	st, out = step("cle-000001")
	assert.Equal(t, 200, st)
	assert.Equal(t, true, out["rejoue"])
	assert.Empty(t, e.d.paths)
	// Clé neuve, même liste : rendue, pas recréée. Même id, autre nom : refusé.
	st, out = step("cle-000002", reply{200, `{"list":{"id":"lotnovembre","name":"Lot de novembre"}}`})
	assert.Equal(t, 200, st)
	assert.Equal(t, true, out["resultat"].(map[string]any)["existait"])
	assert.Len(t, e.d.paths, 1)
	st, out = step("cle-000003", reply{200, `{"list":{"id":"lotnovembre","name":"Clients"}}`})
	assert.Equal(t, 409, st)
	assert.Contains(t, out["reason"], "déjà pris")
	// Lecture en échec : rien n'est créé.
	for _, r := range []reply{{403, `{"error":"droit"}`}, {500, `{}`}, {200, `{}`}} {
		st, _ = step("cle-000004", r)
		assert.Contains(t, []int{403, 503}, st)
		assert.Len(t, e.d.paths, 1)
	}
	st, _ = step("cle-000005", reply{404, `{}`}, reply{403, `{"error":"lists:write"}`})
	assert.Equal(t, 403, st)
	e.d.replies = nil
	for _, bad := range []string{``, `{}`, `{"id":"a b","name":"x"}`, `{"id":"é","name":"x"}`, `{"id":"a","name":" "}`, `{"id":"a","name":"x","is_double_optin":true}`, `{"id":"` + strings.Repeat("a", 33) + `","name":"x"}`} {
		st, _ := e.post("listes.creer", e.hub.token(t, nil), "cle-000006", bad)
		assert.Equal(t, 400, st, bad)
	}
	st, _ = e.post("listes.creer", e.hub.token(t, nil), "", `{"id":"a","name":"x"}`)
	assert.Equal(t, 400, st, "clé requise")
}

// Le Hub signe aujourd'hui l'adresse MCP de la cible : acceptée ici (act exigé), jamais une autre.
func TestJeton_AudienceMCP_AccepteeSiConfiguree(t *testing.T) {
	e := newEnv(t)
	mcpAud := "https://notifuse.test/api/mcp"
	e.mux = http.NewServeMux()
	Mount(e.mux, &Handler{Verifier: &mcp.Verifier{JWKSURL: e.hub.url, Issuer: testIss, Audience: testAud, AlsoAudience: mcpAud}, Store: e.st, Dispatcher: e.d, Log: logger.NewLogger()})
	aud := func(a string) func(*mcp.Claims) { return func(c *mcp.Claims) { c.Audience = jwt.ClaimStrings{a} } }
	st, _ := e.post(imp, e.hub.token(t, aud(mcpAud)), "cle-000001", body)
	assert.Equal(t, 200, st)
	st, _ = e.post(imp, e.hub.token(t, aud(testAud)), "cle-000002", body)
	assert.Equal(t, 200, st)
	st, _ = e.post(imp, e.hub.token(t, aud("https://crm.test/mcp")), "cle-000003", body)
	assert.Equal(t, 401, st)
	st, _ = e.post(imp, e.hub.token(t, func(c *mcp.Claims) { c.Audience = jwt.ClaimStrings{mcpAud}; c.Act = nil }), "cle-000004", body)
	assert.Equal(t, 401, st, "un vrai jeton MCP n'a pas de act")
}
