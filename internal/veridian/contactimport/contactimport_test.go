package contactimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type conn struct {
	db  *sql.DB
	err error
}

func (c conn) GetConnection(context.Context, string) (*sql.DB, error) { return c.db, c.err }

func workspaceDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testpg.DSN(t)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	name := fmt.Sprintf("contactimport_test_%d", time.Now().UnixNano())
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
	require.NoError(t, database.InitializeWorkspaceDatabase(db))
	return db
}

// post joue l'import : l'amont répond ce qu'on lui dit, la base contient ce qu'elle contient.
func post(t *testing.T, c Conn, body, upstream string, code int) (int, map[string]any, http.Header) {
	t.Helper()
	mux := http.NewServeMux()
	var seen string
	mux.HandleFunc(Route, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, len(body)+1)
		n, _ := r.Body.Read(b)
		seen = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amont", "oui")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(upstream))
	})
	Mount(mux, c, logger.NewLoggerWithLevel("disabled"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", Route, strings.NewReader(body)))
	assert.Equal(t, body, seen, "l'amont reçoit la demande telle quelle")
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Header()
}

func report(t *testing.T, out map[string]any) Report {
	t.Helper()
	b, _ := json.Marshal(out["report"])
	var r Report
	require.NoError(t, json.Unmarshal(b, &r))
	return r
}

func TestMount_CeQuiEstVraimentDansLEspace(t *testing.T) {
	db := workspaceDB(t)
	ex := func(q string, a ...any) {
		t.Helper()
		_, err := db.Exec(q, a...)
		require.NoError(t, err, q)
	}
	for _, l := range []string{"l1", "l2"} {
		ex(`INSERT INTO lists (id, name, is_double_optin, is_public, created_at, updated_at) VALUES ($1, $1, false, false, NOW(), NOW())`, l)
	}
	for _, e := range []string{"a@x.fr", "b@x.fr", "c@x.fr"} {
		ex(`INSERT INTO contacts (email, created_at, updated_at) VALUES ($1, NOW(), NOW())`, e)
	}
	ex(`INSERT INTO contact_lists (email, list_id, status) VALUES ('a@x.fr','l1','active'), ('b@x.fr','l1','active'), ('a@x.fr','l2','active'), ('b@x.fr','l2','unsubscribed')`)
	c := conn{db: db}
	three := func(n int) *int { return &n }

	// Le cas d'origine : une erreur par contact, HTTP 200, rien d'écrit. La vraie raison et l'adresse reviennent.
	_, out, _ := post(t, c, `{"workspace_id":"ws","contacts":[{"email":"z@x.fr","custom_number_1":"88"}],"subscribe_to_lists":["l1"]}`,
		`{"operations":[{"email":"","action":"error","error":"invalid contact at index 0: email is required"}]}`, 200)
	r := report(t, out)
	assert.Equal(t, Report{State: StateFailed, Received: 1, Failed: 1, Verified: three(0), Lists: map[string]ListReport{"l1": {}}}, r)
	op := out["operations"].([]any)[0].(map[string]any)
	assert.Equal(t, "z@x.fr", op["email"])
	assert.Contains(t, op["error"], "index 0")
	assert.Contains(t, op["error"], "custom_number_1")
	assert.NotContains(t, op["error"], "email is required")

	// Tout y est.
	st, out, h := post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"},{"email":"b@x.fr"}],"subscribe_to_lists":["l1"]}`,
		`{"operations":[{"email":"a@x.fr","action":"create"},{"email":"b@x.fr","action":"update"}]}`, 200)
	assert.Equal(t, 200, st)
	assert.Equal(t, "oui", h.Get("X-Amont"))
	assert.Equal(t, Report{State: StateComplete, Received: 2, Created: 1, Updated: 1, Verified: three(2), Lists: map[string]ListReport{"l1": {Subscribed: 2}}}, report(t, out))

	// L'amont tait un échec d'inscription à une liste : la relecture le dit. Un désinscrit gardé n'est pas un manque.
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"},{"email":"b@x.fr"},{"email":"c@x.fr"}],"subscribe_to_lists":["l2"]}`,
		`{"operations":[{"email":"a@x.fr","action":"create"},{"email":"b@x.fr","action":"create"},{"email":"c@x.fr","action":"create"}]}`, 200)
	r = report(t, out)
	assert.Equal(t, StatePartial, r.State)
	assert.Equal(t, ListReport{Subscribed: 1, Other: 1, Absent: 1}, r.Lists["l2"])
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"},{"email":"b@x.fr"}],"subscribe_to_lists":["l2"]}`,
		`{"operations":[{"email":"a@x.fr","action":"create"},{"email":"b@x.fr","action":"create"}]}`, 200)
	assert.Equal(t, StateComplete, report(t, out).State, "désinscrit gardé")

	// Une liste inconnue : l'amont y inscrit sans rien dire.
	ex(`INSERT INTO contact_lists (email, list_id, status) VALUES ('a@x.fr','nulle','active')`)
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"}],"subscribe_to_lists":["nulle"]}`, `{"operations":[{"email":"a@x.fr","action":"update"}]}`, 200)
	assert.Equal(t, StatePartial, report(t, out).State)
	assert.True(t, report(t, out).Lists["nulle"].Unknown)

	// L'amont annonce une fiche qui n'y est pas, puis un doublon.
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"},{"email":"fantome@x.fr"}]}`,
		`{"operations":[{"email":"a@x.fr","action":"create"},{"email":"fantome@x.fr","action":"create"}]}`, 200)
	assert.Equal(t, Report{State: StatePartial, Received: 2, Created: 2, Verified: three(1)}, report(t, out))
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"},{"email":"a@x.fr"}]}`, `{"operations":[{"email":"a@x.fr","action":"update"}]}`, 200)
	assert.Equal(t, Report{State: StateComplete, Received: 2, Updated: 1, Duplicates: 1, Verified: three(1)}, report(t, out))

	// Une partie refusée.
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"},{"email":"pas-une-adresse"}]}`,
		`{"operations":[{"email":"pas-une-adresse","action":"error","error":"invalid contact at index 1: invalid email"},{"email":"a@x.fr","action":"update"}]}`, 200)
	assert.Equal(t, StatePartial, report(t, out).State)
	assert.Equal(t, "invalid contact at index 1: invalid email", out["operations"].([]any)[0].(map[string]any)["error"], "une raison déjà juste est gardée")
}

func TestMount_RelectureNonAboutie(t *testing.T) {
	_, out, _ := post(t, conn{err: errors.New("base injoignable")}, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"}]}`, `{"operations":[{"email":"a@x.fr","action":"create"}]}`, 200)
	r := report(t, out)
	assert.Equal(t, StateUnknown, r.State, "jamais « complet » sans relecture")
	assert.Nil(t, r.Verified)
}

// Un refus, une réponse inattendue ou un autre verbe passent tels quels.
func TestMount_LeResteEstIntact(t *testing.T) {
	c := conn{err: errors.New("ne doit pas être lu")}
	st, out, _ := post(t, c, `{"workspace_id":"ws","contacts":[]}`, `{"error":"contacts array is empty"}`, 400)
	assert.Equal(t, 400, st)
	assert.Equal(t, map[string]any{"error": "contacts array is empty"}, out)
	_, out, _ = post(t, c, `{"workspace_id":"ws","contacts":[{"email":"a@x.fr"}]}`, `{"error":"non","operations":[]}`, 403)
	assert.NotContains(t, out, "report", "un refus n'est pas annoté")
	_, out, _ = post(t, c, `pas du json`, `{"ok":true}`, 200)
	assert.Equal(t, map[string]any{"ok": true}, out)

	mux := http.NewServeMux()
	mux.HandleFunc(Route, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusMethodNotAllowed) })
	Mount(mux, c, logger.NewLoggerWithLevel("disabled"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", Route, nil))
	assert.Equal(t, 405, rec.Code)
}

func TestCapability(t *testing.T) {
	for _, c := range platform.Default.List() {
		if c.Name == "contactimport" && c.Wire != nil {
			return
		}
	}
	t.Fatal("capacité non enregistrée")
}
