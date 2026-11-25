package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/app"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mcp"
	"github.com/Notifuse/notifuse/tests/testutil"
)

// « Envoyer vers Mail » (INTEGRATIONS-NATIVES F2) sur l'APPLICATION ENTIÈRE, avec un jeton d'action signé localement : 25 prospects
// donnent 25 contacts étiquetés, le rejeu n'en crée aucun, un désinscrit reste exclu, aucun mail n'entre en file.
func TestVeridianActions_EnvoyerVersMail(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	pad := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(append(make([]byte, 32-len(b)), b...))
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "EC", "crv": "P-256", "kid": "k1", "x": pad(key.X.Bytes()), "y": pad(key.Y.Bytes())}}})
	}))
	defer jwks.Close()
	const iss, aud = "https://hub.test", "https://notifuse.test/api/veridian/actions"
	t.Setenv("VERIDIAN_HUB_JWKS_URL", jwks.URL)
	t.Setenv("VERIDIAN_HUB_ISSUER", iss)
	t.Setenv("VERIDIAN_ACTIONS_AUDIENCE", aud)

	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface { return app.NewApp(cfg) })
	defer func() { suite.Cleanup() }()
	f := suite.DataFactory
	user, err := f.CreateUser()
	require.NoError(t, err)
	ws, err := f.CreateWorkspace()
	require.NoError(t, err)
	require.NoError(t, f.AddUserToWorkspace(user.ID, ws.ID, "owner"))
	stranger, err := f.CreateWorkspace()
	require.NoError(t, err)
	list, err := f.CreateList(ws.ID)
	require.NoError(t, err)
	hubID, sys := uuid.NewString(), suite.DBManager.GetDB()
	_, err = sys.Exec(`UPDATE users SET hub_user_id=$1 WHERE id=$2`, hubID, user.ID)
	require.NoError(t, err)
	for id, tn := range map[string]string{ws.ID: "tenant-a", stranger.ID: "tenant-b"} {
		_, err = sys.Exec(`INSERT INTO veridian_plan (workspace_id, hub_tenant_id) VALUES ($1,$2) ON CONFLICT (workspace_id) DO UPDATE SET hub_tenant_id=$2`, id, tn)
		require.NoError(t, err)
	}
	wsDB, err := suite.DBManager.GetWorkspaceDB(ws.ID)
	require.NoError(t, err)
	// Un désinscrit, connu avant l'import.
	out, err := f.CreateContact(ws.ID, testutil.WithContactEmail("desinscrit@exemple-a.test"))
	require.NoError(t, err)
	_, err = f.CreateContactList(ws.ID, testutil.WithContactListEmail(out.Email), testutil.WithContactListListID(list.ID), testutil.WithContactListStatus(domain.ContactListStatusUnsubscribed))
	require.NoError(t, err)

	token := func(mutate func(*mcp.Claims)) string {
		now := time.Now()
		c := &mcp.Claims{Email: user.Email, EmailVerified: true, Scope: "notifuse:contacts.ecrire", TenantID: "tenant-a", Act: &mcp.Actor{Subject: "app:prospection"},
			RegisteredClaims: jwt.RegisteredClaims{Issuer: iss, Audience: jwt.ClaimStrings{aud}, Subject: hubID, ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(2 * time.Minute))}}
		if mutate != nil {
			mutate(c)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, c)
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(key)
		require.NoError(t, err)
		return s
	}
	post := func(path, tok, idem string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest("POST", suite.ServerManager.GetURL()+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", idem)
		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	const path = "/api/veridian/actions/contacts.importer"
	contacts := []map[string]any{{"email": out.Email, "siren": "900000099"}}
	for i := 0; i < 25; i++ {
		contacts = append(contacts, map[string]any{"email": fmt.Sprintf("contact@entreprise-%02d.test", i), "siren": fmt.Sprintf("9000000%02d", i), "company": "E"})
	}
	body := map[string]any{"list_ids": []string{list.ID}, "contacts": contacts}
	count := func(q string) (n int) {
		require.NoError(t, wsDB.QueryRow(q).Scan(&n), q)
		return n
	}
	tagged := `SELECT COUNT(*) FROM contacts WHERE custom_json_1->>'veridian_source' = 'prospection'`

	// Simulation : rien n'est écrit.
	sim := map[string]any{"list_ids": body["list_ids"], "contacts": contacts, "dry_run": true}
	st, m := post(path, token(nil), "", sim)
	require.Equal(t, 200, st, m)
	assert.Equal(t, 0, count(tagged))

	// L'espace d'un autre, une portée absente, un jeton rejoué, un retiré : refus, rien d'écrit.
	st, _ = post(path, token(func(c *mcp.Claims) { c.TenantID = "tenant-b" }), "geste-0001", body)
	assert.Equal(t, 403, st)
	st, _ = post(path, token(func(c *mcp.Claims) { c.Scope = "crm:tache.ecrire" }), "geste-0001", body)
	assert.Equal(t, 403, st)
	st, _ = post("/api/mcp", token(nil), "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	assert.Equal(t, 401, st, "un jeton d'action n'ouvre pas le MCP")
	assert.Equal(t, 0, count(tagged))

	tok := token(nil)
	st, m = post(path, tok, "geste-0001", body)
	require.Equal(t, 200, st, m)
	res := m["resultat"].(map[string]any)
	assert.EqualValues(t, 25, res["created"], m)
	assert.Equal(t, 25, count(tagged))
	assert.Equal(t, 25, count(`SELECT COUNT(*) FROM contact_lists WHERE list_id='`+list.ID+`' AND status='active' AND deleted_at IS NULL`))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM contact_lists WHERE email='desinscrit@exemple-a.test' AND status='unsubscribed'`))

	st, m = post(path, tok, "geste-0002", body)
	assert.Equal(t, 401, st)
	assert.Equal(t, "jeton_deja_vu", m["error"])
	st, m = post(path, token(nil), "geste-0001", body)
	assert.Equal(t, 200, st)
	assert.Equal(t, true, m["rejoue"])
	assert.EqualValues(t, 25, m["resultat"].(map[string]any)["created"], "le premier résultat")
	// Clé neuve, même lot : l'import dédoublonne de toute façon.
	st, m = post(path, token(nil), "geste-0003", body)
	assert.Equal(t, 200, st)
	assert.EqualValues(t, 0, m["resultat"].(map[string]any)["created"], m)
	assert.Equal(t, 25, count(tagged))

	// Choisir et créer une liste : la liste neuve apparaît, privée, sans double opt-in ; la recréer la rend.
	st, m = post("/api/veridian/actions/listes.creer", token(nil), "geste-l001", map[string]any{"id": "lotnovembre", "name": "Lot de novembre"})
	require.Equal(t, 200, st, m)
	st, m = post("/api/veridian/actions/listes.creer", token(nil), "geste-l002", map[string]any{"id": "lotnovembre", "name": "Lot de novembre"})
	require.Equal(t, 200, st, m)
	assert.Equal(t, true, m["resultat"].(map[string]any)["existait"])
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM lists WHERE id='lotnovembre' AND NOT is_public AND NOT is_double_optin`))
	_, err = wsDB.Exec(`INSERT INTO automations (id, workspace_id, name, status, trigger_config) VALUES ('seq',$1,'seq','live',$2)`, ws.ID, `{"event_kind":"list.subscribed","list_id":"lotnovembre"}`)
	require.NoError(t, err)
	st, m = post("/api/veridian/actions/listes.lister", token(nil), "", map[string]any{})
	require.Equal(t, 200, st, m)
	starts := map[string]any{}
	for _, l := range m["resultat"].(map[string]any)["lists"].([]any) {
		starts[l.(map[string]any)["id"].(string)] = l.(map[string]any)["starts_sequence"]
	}
	assert.Equal(t, true, starts["lotnovembre"])
	assert.Equal(t, false, starts[list.ID])
	st, m = post(path, token(nil), "geste-0005", map[string]any{"list_ids": []string{"lotnovembre"}, "contacts": contacts})
	assert.Equal(t, 409, st, m)
	assert.Equal(t, "would_trigger", m["detail"].(map[string]any)["code"])

	// Inscrire à une séquence depuis le CRM (sa propre portée) : neuf et existant entrent, désinscrit et « ne plus contacter » jamais.
	crm := func(c *mcp.Claims) { c.Scope = "notifuse:sequences.inscrire notifuse:contacts.ecrire"; c.Act.Subject = "app:crm" }
	_, err = wsDB.Exec(`DELETE FROM automations WHERE id='seq'`)
	require.NoError(t, err)
	_, err = wsDB.Exec(`INSERT INTO automations (id, workspace_id, name, status, list_id, trigger_config, root_node_id, nodes) VALUES
		('relance',$1,'Relance','live',$2,'{"event_kind":"email.opened","frequency":"once"}','n1','[{"id":"n1","type":"delay","config":{"duration":5,"unit":"days"},"position":{"x":0,"y":0}}]'),
		('brouillon',$1,'Brouillon','draft','','{"event_kind":"email.opened","frequency":"once"}','n1','[]')`, ws.ID, list.ID)
	require.NoError(t, err)
	st, m = post("/api/veridian/actions/sequences.lister", token(crm), "", map[string]any{})
	require.Equal(t, 200, st, m)
	can := map[string]any{}
	for _, x := range m["resultat"].(map[string]any)["sequences"].([]any) {
		can[x.(map[string]any)["id"].(string)] = x.(map[string]any)["inscriptible"]
	}
	assert.Equal(t, map[string]any{"relance": true, "brouillon": false}, can)

	const enrollPath = "/api/veridian/actions/sequences.inscrire"
	people := []map[string]any{{"email": "neuf@fiche-crm.test", "first_name": "Ana"}, {"email": "contact@entreprise-01.test"}, {"email": out.Email}, {"email": "stop@fiche-crm.test", "ne_plus_contacter": true}}
	enroll := map[string]any{"sequence_id": "relance", "contacts": people}
	in := `SELECT COUNT(*) FROM contact_automations WHERE automation_id='relance'`
	st, _ = post(enrollPath, token(nil), "geste-s000", enroll)
	assert.Equal(t, 403, st, "verser un contact ne donne pas le droit d'inscrire")
	st, m = post(enrollPath, token(crm), "", map[string]any{"sequence_id": "relance", "contacts": people, "dry_run": true})
	require.Equal(t, 200, st, m)
	assert.EqualValues(t, 2, m["resultat"].(map[string]any)["inscrits"], m)
	assert.Equal(t, 0, count(in))
	assert.Equal(t, 0, count(`SELECT COUNT(*) FROM contacts WHERE email='neuf@fiche-crm.test'`), "simulation : aucune fiche")
	for i := 0; i < 2; i++ {
		st, m = post(enrollPath, token(crm), "geste-s001", enroll)
		require.Equal(t, 200, st, m)
		r := m["resultat"].(map[string]any)
		assert.EqualValues(t, 2, r["inscrits"], m)
		assert.Equal(t, map[string]any{"desinscrit": float64(1), "ne_plus_contacter": float64(1)}, r["skipped"])
		assert.Equal(t, i == 1, m["rejoue"])
		assert.Equal(t, 2, count(in))
	}
	st, m = post(enrollPath, token(crm), "geste-s002", enroll)
	require.Equal(t, 200, st, m)
	assert.EqualValues(t, 2, m["resultat"].(map[string]any)["skipped"].(map[string]any)["deja_dans_la_sequence"], m)
	assert.Equal(t, 2, count(in), "clé neuve, mêmes personnes : personne n'entre deux fois")
	assert.Equal(t, 0, count(`SELECT COUNT(*) FROM contacts WHERE email='stop@fiche-crm.test'`))
	assert.Equal(t, 1, count(`SELECT COUNT(*) FROM contact_lists WHERE email='desinscrit@exemple-a.test' AND status='unsubscribed'`), "le désinscrit le reste")
	st, m = post(enrollPath, token(crm), "geste-s003", map[string]any{"sequence_id": "brouillon", "contacts": people[:1]})
	assert.Equal(t, 400, st, m)
	assert.Equal(t, "refus_de_l_app", m["error"])

	// Retiré de l'espace : refus à la demande suivante.
	_, err = sys.Exec(`DELETE FROM user_workspaces WHERE user_id=$1 AND workspace_id=$2`, user.ID, ws.ID)
	require.NoError(t, err)
	st, m = post(path, token(nil), "geste-0004", body)
	assert.Equal(t, 403, st)
	assert.Equal(t, "non_membre", m["error"])

	// Sur l'application entière : une route inconnue ou la mauvaise méthode est un 404, jamais un 200 vide.
	st, m = post("/api/lists.zzz", token(nil), "", map[string]any{})
	assert.Equal(t, 404, st)
	assert.Equal(t, "route_not_found", m["code"])
	st, _ = post("/api/veridian/membres", token(nil), "", map[string]any{})
	assert.Equal(t, 404, st, "GET seulement")

	// Aucun envoi.
	assert.Equal(t, 0, count(`SELECT COUNT(*) FROM email_queue`))
	assert.Equal(t, 0, count(`SELECT COUNT(*) FROM message_history`))
}

// Import groupé (fiche 94) : le compte rendu vient de l'espace, sur l'application entière. Cas d'origine : 806 refus lus comme des réussites.
func TestVeridianContactImport_CompteRenduRelu(t *testing.T) {
	testutil.SkipIfShort(t)
	testutil.SetupTestEnvironment()
	defer testutil.CleanupTestEnvironment()
	suite := testutil.NewIntegrationTestSuite(t, func(cfg *config.Config) testutil.AppInterface { return app.NewApp(cfg) })
	defer func() { suite.Cleanup() }()
	f, client := suite.DataFactory, suite.APIClient
	user, err := f.CreateUser()
	require.NoError(t, err)
	ws, err := f.CreateWorkspace()
	require.NoError(t, err)
	require.NoError(t, f.AddUserToWorkspace(user.ID, ws.ID, "owner"))
	list, err := f.CreateList(ws.ID)
	require.NoError(t, err)
	require.NoError(t, client.Login(user.Email, "password"))
	client.SetWorkspaceID(ws.ID)
	imp := func(contacts []map[string]any, lists ...string) (map[string]any, []any) {
		t.Helper()
		resp, err := client.Post("/api/contacts.import", map[string]any{"workspace_id": ws.ID, "contacts": contacts, "subscribe_to_lists": lists})
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		var m map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
		return m["report"].(map[string]any), m["operations"].([]any)
	}
	r, ops := imp([]map[string]any{{"email": "a@import.test", "custom_number_1": "88"}, {"email": "b@import.test", "custom_number_1": "89"}}, list.ID)
	assert.Equal(t, "echec", r["state"])
	assert.EqualValues(t, 0, r["verified"])
	assert.Equal(t, "a@import.test", ops[0].(map[string]any)["email"])
	assert.Contains(t, ops[0].(map[string]any)["error"], "custom_number_1")

	r, _ = imp([]map[string]any{{"email": "A@import.test", "custom_number_1": 88}, {"email": "b@import.test"}}, list.ID)
	assert.Equal(t, "complet", r["state"], r)
	assert.EqualValues(t, 2, r["verified"])
	assert.EqualValues(t, 2, r["lists"].(map[string]any)[list.ID].(map[string]any)["subscribed"])

	r, _ = imp([]map[string]any{{"email": "a@import.test"}}, "listeabsente")
	assert.Equal(t, "partiel", r["state"], "une liste qui n'existe pas : l'amont le tait, la relecture le dit")
	r, _ = imp([]map[string]any{{"email": "a@import.test"}, {"email": "c@import.test", "custom_number_1": "x"}})
	assert.Equal(t, "partiel", r["state"])
}
