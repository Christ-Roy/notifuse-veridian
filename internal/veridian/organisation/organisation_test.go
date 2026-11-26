package organisation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const secret = "hub-secret-test"

func systemDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testpg.DSN(t)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	name := fmt.Sprintf("orga_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + pq.QuoteIdentifier(name))
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE " + pq.QuoteIdentifier(name) + " WITH (FORCE)")
		_ = admin.Close()
	})
	require.NoError(t, database.InitializeDatabase(db, ""))
	_, err = veridianmig.Default.ApplySystem(context.Background(), db)
	require.NoError(t, err)
	for ws, tn := range map[string]string{"alpha": "t-alpha", "parti": "t-parti"} {
		_, err = db.Exec(`INSERT INTO workspaces (id,name,settings,created_at,updated_at) VALUES ($1,'Local','{"timezone":"UTC","logo_url":"https://local.test/l.png","website_url":"https://w.test","encrypted_secret_key":"gardé"}',NOW(),NOW())`, ws)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO veridian_plan (workspace_id,hub_tenant_id) VALUES ($1,$2)`, ws, tn)
		require.NoError(t, err)
	}
	_, err = db.Exec(`UPDATE veridian_plan SET deleted_at=NOW() WHERE workspace_id='parti'`)
	require.NoError(t, err)
	return db
}

type fakeAuth struct{ err error }

func (f fakeAuth) AuthenticateUserForWorkspace(ctx context.Context, ws string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	return ctx, &domain.User{ID: "u"}, &domain.UserWorkspace{WorkspaceID: ws}, f.err
}

type fakeSubs struct {
	domain.WebhookSubscriptionRepository
	subs    map[string]*domain.WebhookSubscription
	listErr error
	calls   []string
}

func (f *fakeSubs) List(context.Context, string) ([]*domain.WebhookSubscription, error) {
	out := []*domain.WebhookSubscription{}
	for _, s := range f.subs {
		out = append(out, s)
	}
	return out, f.listErr
}
func (f *fakeSubs) Create(_ context.Context, ws string, s *domain.WebhookSubscription) error {
	f.calls = append(f.calls, "create "+ws)
	f.subs[s.ID] = s
	return nil
}
func (f *fakeSubs) Update(_ context.Context, ws string, s *domain.WebhookSubscription) error {
	f.calls = append(f.calls, "update "+ws)
	return nil
}
func (f *fakeSubs) Delete(_ context.Context, ws, id string) error {
	f.calls = append(f.calls, "delete "+ws)
	delete(f.subs, id)
	return nil
}

type env struct {
	db    *sql.DB
	mux   *http.ServeMux
	h     *Handler
	subs  *fakeSubs
	added []Member
	addKO bool
}

func newEnv(t *testing.T) *env {
	e := &env{db: systemDB(t), mux: http.NewServeMux(), subs: &fakeSubs{subs: map[string]*domain.WebhookSubscription{
		"client": {ID: "client", URL: "https://client.test", Enabled: true}}}}
	e.h = &Handler{Store: &SQLStore{DB: e.db}, Auth: fakeAuth{}, Subs: e.subs, HubURL: "https://hub.test/", Log: logger.NewLoggerWithLevel("disabled"),
		AddMember: func(_ context.Context, ws string, m Member) error {
			if e.addKO {
				return errors.New("refus")
			}
			e.added = append(e.added, m)
			return nil
		}}
	Mount(e.mux, e.h, secret, func(h http.Handler) http.Handler { return h })
	return e
}

func (e *env) call(method, path, body string, sign bool) (int, map[string]any) {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if sign {
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "." + body))
		r.Header.Set("X-Veridian-Timestamp", ts)
		r.Header.Set("X-Veridian-Hub-Signature", hex.EncodeToString(mac.Sum(nil)))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, r)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (e *env) space(t *testing.T) (name, logo, settings string) {
	require.NoError(t, e.db.QueryRow(`SELECT name, settings->>'logo_url', settings::text FROM workspaces WHERE id='alpha'`).Scan(&name, &logo, &settings))
	return
}

const sync = "/api/hub/organisations/sync"

func push(version int, extra string) string {
	return fmt.Sprintf(`{"hub_tenant_id":"t-alpha","version":%d%s}`, version, extra)
}

const full = `,"nom":"Société A","type":"equipe","modules":["mail"],"inconnu":1,"reglages":{"notifuse":{"plafond":9999}},"marque":{"nom_affiche":"Marque A","logo_url":"https://cdn.test/a.png","favicon_url":"https://cdn.test/f.ico","couleur_primaire":"#112233","couleur_accent":"#445566"}`

func TestSync_AppliqueDansLOrdre_SansRienVider(t *testing.T) {
	e := newEnv(t)
	st, out := e.call("POST", sync, push(5, full), true)
	require.Equal(t, 200, st, out)
	assert.Equal(t, true, out["applied"])
	assert.Equal(t, "alpha", out["workspace_id"])
	name, logo, settings := e.space(t)
	assert.Equal(t, "Marque A", name, "le nom affiché l'emporte")
	assert.Equal(t, "https://cdn.test/a.png", logo)
	for _, kept := range []string{"https://w.test", "gardé", "UTC"} {
		assert.Contains(t, settings, kept, "le reste des réglages est intact")
	}
	assert.NotContains(t, settings, "9999", "aucun réglage d'envoi ne passe par ici")

	st, out = e.call("POST", sync, push(4, `,"nom":"Vieux"`), true)
	assert.Equal(t, 200, st)
	assert.Equal(t, false, out["applied"])
	assert.Equal(t, "stale", out["reason"])
	assert.EqualValues(t, 5, out["version"])
	st, out = e.call("POST", sync, push(5, full), true)
	assert.Equal(t, 200, st)
	assert.Equal(t, false, out["applied"], "même version : déjà là")
	assert.Equal(t, "unchanged", out["reason"], "le mot que le Hub lit")
	name, _, _ = e.space(t)
	assert.Equal(t, "Marque A", name)

	st, _ = e.call("POST", sync, push(6, `,"nom":"","marque":{}`), true)
	assert.Equal(t, 200, st)
	name, logo, _ = e.space(t)
	assert.Equal(t, "Marque A", name, "une valeur absente ne vide rien")
	assert.Equal(t, "https://cdn.test/a.png", logo)
	st, _ = e.call("POST", sync, push(7, `,"nom":"Société A"`), true)
	assert.Equal(t, 200, st)
	name, _, _ = e.space(t)
	assert.Equal(t, "Marque A", name, "le nom du compte au Hub (nom) ne renomme jamais l'espace")
}

// Un nom que personne n'a choisi ne renomme pas l'espace ; un nom choisi, si ; rien d'autre ne bouge (identifiant, profils, adresses d'envoi, membres).
func TestSync_RenommageSeulementSiChoisi(t *testing.T) {
	e := newEnv(t)
	const integ = `[{"id": "p1", "name": "Profil", "type": "email", "email_provider": {"kind": "smtp", "senders": [{"id": "s", "name": "Léa", "email": "lea@envoi.test"}]}}]`
	_, err := e.db.Exec(`UPDATE workspaces SET integrations=$1, settings = settings || '{"marketing_email_provider_id":"p1","custom_endpoint_url":"https://mail.envoi.test"}' WHERE id='alpha'`, integ)
	require.NoError(t, err)
	snap := func() string {
		var v string
		require.NoError(t, e.db.QueryRow(`SELECT id || integrations::text || (settings - 'logo_url')::text || created_at::text || (SELECT count(*) FROM user_workspaces)::text FROM workspaces WHERE id='alpha'`).Scan(&v))
		return v
	}
	before := snap()
	for i, auto := range []string{"robert.brunon's Workspace", "robert.brunon’s Workspace"} {
		_, out := e.call("POST", sync, push(i+1, fmt.Sprintf(`,"nom":%q,"marque":{"nom_affiche":%q}`, auto, auto)), true)
		assert.Equal(t, true, out["applied"])
		name, _, _ := e.space(t)
		assert.Equal(t, "Local", name, auto)
		_, out = e.call("GET", "/api/veridian/organisation?workspace_id=alpha", "", false)
		assert.Equal(t, false, out["managed"], "rien n'est verrouillé sans nom choisi")
	}
	_, _ = e.call("POST", sync, push(3, `,"nom":"compte","marque":{"nom_affiche":"Green Beam Craft"}`), true)
	name, _, _ := e.space(t)
	assert.Equal(t, "Green Beam Craft", name)
	assert.Equal(t, before, snap(), "identifiant, profils, adresses d'envoi, réglages et membres intacts")
}

// Le Hub ne dit « appliqué » qu'après avoir relu la version que l'app sert.
func TestVersion_RelueParLeHub(t *testing.T) {
	e := newEnv(t)
	u := func(tn string) string { return sync + "?hub_tenant_id=" + tn }
	st, out := e.call("GET", u("t-alpha"), "", true)
	assert.Equal(t, 200, st, out)
	assert.EqualValues(t, 0, out["version"], "rien reçu")
	_, _ = e.call("POST", sync, push(1782259200000, full), true)
	st, out = e.call("GET", u("t-alpha"), "", true)
	assert.Equal(t, 200, st)
	assert.EqualValues(t, 1782259200000, out["version"])
	assert.Equal(t, "Marque A", out["name"])
	st, _ = e.call("GET", u("t-alpha"), "", false)
	assert.Equal(t, 401, st, "sans signature")
	st, _ = e.call("GET", u("inconnu"), "", true)
	assert.Equal(t, 404, st)
	st, _ = e.call("GET", sync, "", true)
	assert.Equal(t, 400, st)
	_, err := e.db.Exec(`UPDATE veridian_organisation SET corps = jsonb_set(corps, '{hub_tenant_id}', '"autre"')`)
	require.NoError(t, err)
	_, out = e.call("GET", u("t-alpha"), "", true)
	assert.EqualValues(t, 0, out["version"], "poussée d'un autre tenant")
}

func TestSync_Refus(t *testing.T) {
	e := newEnv(t)
	st, _ := e.call("POST", sync, push(1, full), false)
	assert.Equal(t, 401, st, "sans signature")
	for _, tn := range []string{"t-parti", "inconnu", "alpha"} {
		st, out := e.call("POST", sync, `{"hub_tenant_id":"`+tn+`","version":1,"nom":"X"}`, true)
		assert.Equal(t, 404, st, tn)
		assert.Equal(t, "espace_non_relie", out["error"])
	}
	for _, bad := range []string{`{`, `{}`, push(0, ""), push(-1, ""), push(1, `,"marque":{"logo_url":"http://x.test/a.png"}`), push(1, `,"marque":{"favicon_url":"javascript:alert(1)"}`),
		push(1, `,"marque":{"logo_url":"https://u:p@x.test/a.png"}`), push(1, `,"marque":{"couleur_primaire":"red;}"}`), push(1, `,"marque":{"couleur_accent":"#12345"}`),
		push(1, `,"nom":"`+strings.Repeat("a", 256)+`"`), push(1, `,"membres":[`+strings.Repeat(`{},`, 200)+`{}]`)} {
		st, out := e.call("POST", sync, bad, true)
		assert.Equal(t, 400, st, bad[:min(80, len(bad))])
		assert.Equal(t, "invalid_payload", out["error"])
	}
	st, _ = e.call("POST", sync, push(1, `,"nom":"`+strings.Repeat("a", maxBody)+`"`), true)
	assert.Contains(t, []int{413, 401}, st)
	name, _, _ := e.space(t)
	assert.Equal(t, "Local", name)
}

func TestSync_MembresAdditifs(t *testing.T) {
	e := newEnv(t)
	_, err := e.db.Exec(`INSERT INTO users (id,type,email,hub_user_id,created_at,updated_at) VALUES ('00000000-0000-0000-0000-000000000001','user','ancienne@x.test','6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01',NOW(),NOW())`)
	require.NoError(t, err)
	_, err = e.db.Exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,created_at,updated_at) VALUES ('00000000-0000-0000-0000-000000000001','alpha','owner',NOW(),NOW())`)
	require.NoError(t, err)
	// h1 a changé d'adresse au Hub : toujours membre. La personne neuve a repris son ancienne adresse : elle est ajoutée quand même (fiche 89).
	members := `,"membres":[{"hub_user_id":"6B3C1F4E-2a51-4c1e-9a77-0d9f2c5b1a01","email":"neuve-adresse@x.test","role":"member"},{"hub_user_id":"0f9d2c11-77aa-4b8e-8f10-5f2d6a1c9b02","email":"ancienne@x.test","role":"admin"}]`
	e.addKO = true
	_, out := e.call("POST", sync, push(1, members), true)
	assert.EqualValues(t, 1, out["membres_refuses"])
	e.addKO = false
	_, out = e.call("POST", sync, push(1, members), true)
	assert.EqualValues(t, 1, out["membres_ajoutes"], "rattrapé au rejeu de la même version")
	require.Len(t, e.added, 1, "un membre déjà là n'est pas touché, son rôle non plus")
	assert.Equal(t, "0f9d2c11-77aa-4b8e-8f10-5f2d6a1c9b02", e.added[0].HubUserID)
	_, _ = e.call("POST", sync, push(3, ""), true)
	e.added = nil
	_, _ = e.call("POST", sync, push(2, members), true)
	assert.Empty(t, e.added, "une version ancienne n'ajoute personne")
}

func TestGet_MarquePourUnMembre(t *testing.T) {
	e := newEnv(t)
	st, out := e.call("GET", "/api/veridian/organisation?workspace_id=alpha", "", false)
	assert.Equal(t, 200, st)
	assert.Equal(t, map[string]any{"managed": false}, out)
	_, _ = e.call("POST", sync, push(1, `,"nom":"","marque":{"couleur_primaire":"#112233"}`), true)
	_, out = e.call("GET", "/api/veridian/organisation?workspace_id=alpha", "", false)
	assert.Equal(t, false, out["managed"], "sans nom du Hub, rien n'est verrouillé dans les réglages")
	assert.Equal(t, "#112233", out["brand"].(map[string]any)["couleur_primaire"], "la couleur passe quand même")
	_, _ = e.call("POST", sync, push(2, full), true)
	_, out = e.call("GET", "/api/veridian/organisation?workspace_id=alpha", "", false)
	assert.Equal(t, true, out["managed"])
	assert.Equal(t, "Marque A", out["name"])
	assert.Equal(t, "#112233", out["brand"].(map[string]any)["couleur_primaire"])
	raw, _ := json.Marshal(out)
	assert.NotContains(t, string(raw), "plafond")
	e.h.Auth = fakeAuth{err: errors.New("non")}
	st, _ = e.call("GET", "/api/veridian/organisation?workspace_id=alpha", "", false)
	assert.Equal(t, 403, st)
	st, _ = e.call("GET", "/api/veridian/organisation", "", false)
	assert.Equal(t, 400, st)
}

func TestAbonnement(t *testing.T) {
	e := newEnv(t)
	const path = "/api/hub/evenements/abonnement"
	on, off := `{"hub_tenant_id":"t-alpha","actif":true}`, `{"hub_tenant_id":"t-alpha","actif":false}`
	st, _ := e.call("POST", path, on, false)
	assert.Equal(t, 401, st)
	for _, bad := range []string{`{}`, `{"hub_tenant_id":"t-alpha"}`, `{"actif":true}`} {
		st, _ = e.call("POST", path, bad, true)
		assert.Equal(t, 400, st, bad)
	}
	st, _ = e.call("POST", path, `{"hub_tenant_id":"t-parti","actif":true}`, true)
	assert.Equal(t, 404, st)
	assert.Empty(t, e.subs.calls)

	st, out := e.call("POST", path, on, true)
	require.Equal(t, 200, st, out)
	assert.Equal(t, "https://hub.test/api/evenements/notifuse", out["url"], "l'adresse du Hub, jamais celle de l'appelant")
	assert.Regexp(t, `^whsec_[A-Za-z0-9+/]{43}=$`, out["secret"])
	assert.Equal(t, []any{"message.received"}, out["events"])
	first := out["secret"]
	_, out = e.call("POST", path, on, true)
	assert.Equal(t, first, out["secret"], "rejouable : le même abonnement")
	assert.Equal(t, []string{"create alpha"}, e.subs.calls)

	// Modifié à l'écran : remis d'équerre, le secret ne change pas.
	s := e.subs.subs[hubSubscriptionID]
	s.URL, s.Enabled = "https://ailleurs.test", false
	_, out = e.call("POST", path, on, true)
	assert.Equal(t, "https://hub.test/api/evenements/notifuse", s.URL)
	assert.True(t, s.Enabled)
	assert.Equal(t, first, out["secret"])
	s.Settings.EventTypes = []string{"email.sent"}
	_, _ = e.call("POST", path, on, true)
	assert.Equal(t, []string{"message.received"}, s.Settings.EventTypes)

	_, out = e.call("POST", path, off, true)
	assert.Equal(t, false, out["actif"])
	_, out = e.call("POST", path, off, true)
	assert.Equal(t, false, out["actif"], "éteindre deux fois")
	assert.Contains(t, e.subs.subs, "client", "l'abonnement du client n'est jamais touché")
	assert.Equal(t, []string{"create alpha", "update alpha", "update alpha", "delete alpha"}, e.subs.calls)
	e.subs.listErr = errors.New("x")
	st, _ = e.call("POST", path, on, true)
	assert.Equal(t, 500, st)
	assert.Len(t, e.subs.calls, 4, "lecture en échec : rien n'est créé")
}

func TestSyncMember_RejoueLaRouteSignee(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	// La signature est vérifiée par le VRAI middleware : v2 (liée à la méthode et au chemin, 26/11), usage unique.
	mux.Handle("POST /api/tenants/{id}/sync-member", middleware.VeridianHMACMiddleware(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(r.Body)
		_ = json.Unmarshal(raw.Bytes(), &got)
		assert.Equal(t, "2", r.Header.Get(middleware.VeridianSigVersionHeader))
		if r.PathValue("id") != "alpha" {
			w.WriteHeader(404)
		}
	})))
	add := syncMember(mux, secret)
	require.NoError(t, add(context.Background(), "alpha", Member{HubUserID: "h", Email: "a@x.test", Role: "owner"}))
	assert.Equal(t, map[string]any{"email": "a@x.test", "hub_user_id": "h", "email_proven": false, "role": "member"}, got, "jamais un propriétaire par cette voie, jamais une attestation supposée")
	// Lue dans le corps du Hub et transmise telle quelle.
	var m Member
	require.NoError(t, json.Unmarshal([]byte(`{"hub_user_id":"h","email":"a@x.test","email_proven":true,"role":"admin"}`), &m))
	require.NoError(t, add(context.Background(), "alpha", m))
	assert.Equal(t, true, got["email_proven"])
	assert.Equal(t, "admin", got["role"])
	require.Error(t, add(context.Background(), "beta", Member{HubUserID: "h", Email: "a@x.test"}))
	require.Error(t, add(context.Background(), "alpha", Member{Email: "a@x.test"}))
}
