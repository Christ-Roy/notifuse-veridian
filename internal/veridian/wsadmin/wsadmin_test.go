package wsadmin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const testSecret = "hub-secret-test"

// Les personnes du Hub (UUID) et leurs comptes ici.
const (
	hubOwner   = "11111111-0000-0000-0000-000000000001"
	hubMember  = "11111111-0000-0000-0000-000000000002"
	hubOther   = "11111111-0000-0000-0000-000000000003" // compte relié, jamais membre de l'espace
	hubKey     = "11111111-0000-0000-0000-000000000004" // une clé API qui porte un hub_user_id
	hubGone    = "11111111-0000-0000-0000-000000000005" // supprimé au Hub
	hubUnknown = "11111111-0000-0000-0000-000000000099" // aucun compte ici
	hubActor   = "22222222-0000-0000-0000-000000000001"
	ws         = "wsalpha"
)

func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

type fixture struct {
	db  *sql.DB
	mux *http.ServeMux
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testpg.System(t, func(db *sql.DB) error {
		if err := database.InitializeDatabase(db, ""); err != nil {
			return err
		}
		_, err := veridianmig.Default.ApplySystem(context.Background(), db)
		return err
	})
	exec := func(q string, args ...interface{}) {
		_, err := db.Exec(q, args...)
		require.NoError(t, err, q)
	}
	for _, w := range []string{ws, "wsparti"} {
		exec(`INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ($1,$1,NOW(),NOW())`, w)
		exec(`INSERT INTO veridian_plan (workspace_id) VALUES ($1)`, w)
	}
	exec(`UPDATE veridian_plan SET deleted_at=NOW() WHERE workspace_id='wsparti'`)
	user := func(n int, kind, hub string) {
		exec(`INSERT INTO users (id,type,email,name,hub_user_id,created_at,updated_at) VALUES ($1,$2,$3,'n',$4,NOW(),NOW())`,
			uid(n), kind, fmt.Sprintf("u%d@x.test", n), hub)
	}
	user(1, "user", hubOwner)
	user(2, "user", hubMember)
	user(3, "user", hubOther)
	user(4, "api_key", hubKey)
	user(5, "user", hubGone)
	exec(`UPDATE users SET hub_deleted_at=NOW() WHERE id=$1`, uid(5))
	join := func(n int, w, role string) {
		exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,$2,$3,'{"contacts":{"read":true,"write":true}}',NOW(),NOW())`,
			uid(n), w, role)
	}
	join(1, ws, "owner")
	join(2, ws, "member")
	join(4, ws, "member")
	join(5, ws, "member")
	join(2, "wsparti", "member")
	m := http.NewServeMux()
	Mount(m, &Handler{Store: &Store{DB: db}, Log: logger.NewLogger()}, testSecret)
	return &fixture{db: db, mux: m}
}

func sign(secret, ts, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func (f *fixture) do(method, path, body string) *httptest.ResponseRecorder {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set("X-Veridian-Hub-Signature", sign(testSecret, ts, body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, r)
	return rec
}

func body(w, hub, action string, reinstate bool) string {
	b, _ := json.Marshal(map[string]interface{}{"workspace_id": w, "hub_user_id": hub, "action": action,
		"actor_hub_user_id": hubActor, "reason": "test", "reinstate": reinstate})
	return string(b)
}

func (f *fixture) post(w, hub, action string, reinstate bool) (int, Result, string) {
	rec := f.do("POST", "/api/hub/workspace-admins", body(w, hub, action, reinstate))
	var res Result
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	var e struct{ Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return rec.Code, res, e.Code
}

func (f *fixture) role(t *testing.T, n int, w string) string {
	t.Helper()
	var r string
	err := f.db.QueryRow(`SELECT role FROM user_workspaces WHERE user_id=$1 AND workspace_id=$2`, uid(n), w).Scan(&r)
	if err == sql.ErrNoRows {
		return ""
	}
	require.NoError(t, err)
	return r
}

func (f *fixture) count(t *testing.T, q string, args ...interface{}) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.QueryRow(q, args...).Scan(&n))
	return n
}

func (f *fixture) ledgerRows(t *testing.T, n int) int {
	return f.count(t, `SELECT COUNT(*) FROM veridian_workspace_admins WHERE workspace_id=$1 AND user_id=$2`, ws, uid(n))
}

func (f *fixture) events(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT action||':'||result||':'||role_before||'>'||role_after FROM veridian_workspace_admin_events ORDER BY id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

func TestGrant_MembreDevientAdmin_RejoueSansChangement(t *testing.T) {
	f := newFixture(t)
	code, res, _ := f.post(ws, hubMember, "grant", false)
	require.Equal(t, 200, code)
	assert.Equal(t, Result{WorkspaceID: ws, HubUserID: hubMember, RoleBefore: "member", RoleAfter: "admin", Changed: true}, res)
	assert.Equal(t, "admin", f.role(t, 2, ws))
	var perms string
	require.NoError(t, f.db.QueryRow(`SELECT permissions::text FROM user_workspaces WHERE user_id=$1 AND workspace_id=$2`, uid(2), ws).Scan(&perms))
	assert.Contains(t, perms, "contacts", "les permissions ne sont pas touchées")

	code, res, _ = f.post(ws, hubMember, "grant", false)
	require.Equal(t, 200, code)
	assert.False(t, res.Changed, "rejoué : rien ne change")
	assert.Equal(t, "admin", res.RoleAfter)
	assert.Equal(t, 1, f.ledgerRows(t, 2))
}

func TestGrant_SurProprietaire_ResteOwner_ChangedUneFois(t *testing.T) {
	f := newFixture(t)
	code, res, _ := f.post(ws, hubOwner, "grant", false)
	require.Equal(t, 200, code)
	assert.Equal(t, "owner", res.RoleBefore)
	assert.Equal(t, "owner", res.RoleAfter)
	assert.True(t, res.Changed, "la désignation est notée")
	assert.Equal(t, "owner", f.role(t, 1, ws), "un propriétaire n'est jamais touché")
	assert.Equal(t, 1, f.ledgerRows(t, 1))

	code, res, _ = f.post(ws, hubOwner, "grant", false)
	require.Equal(t, 200, code)
	assert.False(t, res.Changed)
	assert.Equal(t, "owner", f.role(t, 1, ws))
}

func TestGrant_Refus404(t *testing.T) {
	f := newFixture(t)
	for name, c := range map[string]struct{ w, hub, code string }{
		"non membre":          {ws, hubOther, "not_a_member"},
		"compte inconnu":      {ws, hubUnknown, "user_not_in_app"},
		"clé API jamais":      {ws, hubKey, "user_not_in_app"},
		"supprimé au Hub":     {ws, hubGone, "user_not_in_app"},
		"espace supprimé":     {"wsparti", hubMember, "tenant_not_found"},
		"espace inexistant":   {"nulpart", hubMember, "tenant_not_found"},
		"non membre ailleurs": {"wsparti", hubOther, "tenant_not_found"},
	} {
		code, _, got := f.post(c.w, c.hub, "grant", false)
		assert.Equal(t, 404, code, name)
		assert.Equal(t, c.code, got, name)
	}
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM veridian_workspace_admins`), "aucun refus n'écrit dans le registre")
	assert.Equal(t, "member", f.role(t, 4, ws), "la clé API reste membre")
	assert.Equal(t, "member", f.role(t, 5, ws))
}

func TestGrant_Retire_NeRevientQueSurReinstate(t *testing.T) {
	f := newFixture(t)
	code, _, _ := f.post(ws, hubMember, "grant", false)
	require.Equal(t, 200, code)

	_, err := f.db.Exec(`DELETE FROM user_workspaces WHERE user_id=$1 AND workspace_id=$2`, uid(2), ws)
	require.NoError(t, err)

	code, _, got := f.post(ws, hubMember, "grant", false)
	assert.Equal(t, 409, code)
	assert.Equal(t, "admin_withdrawn", got)

	code, _, got = f.post(ws, hubMember, "grant", true)
	assert.Equal(t, 404, code, "reinstate sans appartenance : rien à rétablir")
	assert.Equal(t, "not_a_member", got)
	assert.Equal(t, "", f.role(t, 2, ws), "jamais de ligne créée")

	_, err = f.db.Exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,$2,'member','{}',NOW(),NOW())`, uid(2), ws)
	require.NoError(t, err)

	code, _, got = f.post(ws, hubMember, "grant", false)
	assert.Equal(t, 409, code, "ré-ajouté comme simple membre : toujours retiré")
	assert.Equal(t, "admin_withdrawn", got)
	assert.Equal(t, "member", f.role(t, 2, ws))

	code, res, _ := f.post(ws, hubMember, "grant", true)
	require.Equal(t, 200, code)
	assert.True(t, res.Changed)
	assert.Equal(t, "admin", res.RoleAfter)
	assert.Equal(t, "admin", f.role(t, 2, ws))
}

func TestRevoke_AdminDevientMembre_Rejoue(t *testing.T) {
	f := newFixture(t)
	require.Equal(t, 200, func() int { c, _, _ := f.post(ws, hubMember, "grant", false); return c }())

	code, res, _ := f.post(ws, hubMember, "revoke", false)
	require.Equal(t, 200, code)
	assert.Equal(t, Result{WorkspaceID: ws, HubUserID: hubMember, RoleBefore: "admin", RoleAfter: "member", Changed: true}, res)
	assert.Equal(t, "member", f.role(t, 2, ws))
	var perms string
	require.NoError(t, f.db.QueryRow(`SELECT permissions::text FROM user_workspaces WHERE user_id=$1 AND workspace_id=$2`, uid(2), ws).Scan(&perms))
	assert.Contains(t, perms, "contacts")

	code, res, _ = f.post(ws, hubMember, "revoke", false)
	require.Equal(t, 200, code)
	assert.False(t, res.Changed, "rejoué")
	assert.Equal(t, "member", f.role(t, 2, ws))

	// Un retrait est un retrait : le rôle ne revient pas sans reinstate.
	code, _, got := f.post(ws, hubMember, "grant", false)
	assert.Equal(t, 409, code)
	assert.Equal(t, "admin_withdrawn", got)
}

func TestRevoke_Proprietaire409_EtJamaisDesigneSansTombe(t *testing.T) {
	f := newFixture(t)
	code, _, got := f.post(ws, hubOwner, "revoke", false)
	assert.Equal(t, 409, code)
	assert.Equal(t, "is_owner", got)
	assert.Equal(t, "owner", f.role(t, 1, ws))

	code, res, _ := f.post(ws, hubMember, "revoke", false)
	require.Equal(t, 200, code)
	assert.False(t, res.Changed)
	assert.Equal(t, 0, f.ledgerRows(t, 2), "aucune tombe pour qui n'a jamais été désigné")

	code, res, _ = f.post(ws, hubMember, "grant", false)
	require.Equal(t, 200, code, "il peut encore être désigné sans reinstate")
	assert.Equal(t, "admin", res.RoleAfter)

	code, _, got = f.post(ws, hubOther, "revoke", false)
	assert.Equal(t, 404, code)
	assert.Equal(t, "not_a_member", got)
}

func TestJournal_UneLignePourChaqueDecisionEtChaqueRefus(t *testing.T) {
	f := newFixture(t)
	f.post(ws, hubMember, "grant", false)        // ok
	f.post(ws, hubMember, "grant", false)        // unchanged
	f.post(ws, hubOwner, "revoke", false)        // is_owner
	f.post(ws, hubOther, "grant", false)         // not_a_member
	f.post(ws, hubUnknown, "grant", false)       // user_not_in_app
	f.post("nulpart", hubMember, "grant", false) // tenant_not_found
	f.post(ws, hubMember, "revoke", false)       // ok
	f.post(ws, hubMember, "grant", false)        // admin_withdrawn
	assert.Equal(t, []string{
		"grant:ok:member>admin",
		"grant:unchanged:admin>admin",
		"revoke:is_owner:owner>owner",
		"grant:not_a_member:>",
		"grant:user_not_in_app:>",
		"grant:tenant_not_found:>",
		"revoke:ok:admin>member",
		"grant:admin_withdrawn:member>member",
	}, f.events(t))

	var actor, reason string
	var reinstate bool
	require.NoError(t, f.db.QueryRow(`SELECT actor_hub_user_id, reason, reinstate FROM veridian_workspace_admin_events ORDER BY id LIMIT 1`).Scan(&actor, &reason, &reinstate))
	assert.Equal(t, hubActor, actor)
	assert.Equal(t, "test", reason)
	assert.False(t, reinstate)
}

func TestPost_CorpsInvalides_400_SansEcritureNiJournal(t *testing.T) {
	f := newFixture(t)
	cases := map[string]string{
		"champ email":        `{"workspace_id":"` + ws + `","hub_user_id":"` + hubMember + `","action":"grant","actor_hub_user_id":"` + hubActor + `","reason":"r","email":"u2@x.test"}`,
		"champ inconnu":      `{"workspace_id":"` + ws + `","hub_user_id":"` + hubMember + `","action":"grant","actor_hub_user_id":"` + hubActor + `","reason":"r","reinstat":true}`,
		"json cassé":         `{`,
		"adresse en hub id":  `{"workspace_id":"` + ws + `","hub_user_id":"u2@x.test","action":"grant","actor_hub_user_id":"` + hubActor + `","reason":"r"}`,
		"acteur absent":      `{"workspace_id":"` + ws + `","hub_user_id":"` + hubMember + `","action":"grant","reason":"r"}`,
		"raison vide":        `{"workspace_id":"` + ws + `","hub_user_id":"` + hubMember + `","action":"grant","actor_hub_user_id":"` + hubActor + `","reason":"  "}`,
		"raison trop longue": `{"workspace_id":"` + ws + `","hub_user_id":"` + hubMember + `","action":"grant","actor_hub_user_id":"` + hubActor + `","reason":"` + string(bytes.Repeat([]byte("x"), 501)) + `"}`,
		"action inconnue":    `{"workspace_id":"` + ws + `","hub_user_id":"` + hubMember + `","action":"promote","actor_hub_user_id":"` + hubActor + `","reason":"r"}`,
		"espace vide":        `{"workspace_id":"","hub_user_id":"` + hubMember + `","action":"grant","actor_hub_user_id":"` + hubActor + `","reason":"r"}`,
	}
	for name, b := range cases {
		rec := f.do("POST", "/api/hub/workspace-admins", b)
		assert.Equal(t, 400, rec.Code, name)
		assert.Contains(t, rec.Body.String(), "invalid_payload", name)
	}
	assert.Equal(t, "member", f.role(t, 2, ws))
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM veridian_workspace_admins`))
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM veridian_workspace_admin_events`))
}

func TestRoutes_SignatureEtSecret(t *testing.T) {
	f := newFixture(t)
	b := body(ws, hubMember, "grant", false)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/hub/workspace-admins"},
		{"GET", "/api/hub/workspace-admins?workspace_id=" + ws},
	} {
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(b)))
		assert.Equal(t, 401, rec.Code, "sans signature "+tc.path)

		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(b))
		r.Header.Set("X-Veridian-Timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
		r.Header.Set("X-Veridian-Hub-Signature", "deadbeef")
		rec = httptest.NewRecorder()
		f.mux.ServeHTTP(rec, r)
		assert.Equal(t, 401, rec.Code, "fausse signature "+tc.path)

		// signée avec un autre secret
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		r = httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(b))
		r.Header.Set("X-Veridian-Timestamp", ts)
		r.Header.Set("X-Veridian-Hub-Signature", sign("autre", ts, b))
		rec = httptest.NewRecorder()
		f.mux.ServeHTTP(rec, r)
		assert.Equal(t, 401, rec.Code, "mauvais secret "+tc.path)
	}
	assert.Equal(t, "member", f.role(t, 2, ws), "rien n'est écrit sans signature")
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM veridian_workspace_admin_events`))

	// secret non configuré : 503, jamais une exécution
	m := http.NewServeMux()
	Mount(m, &Handler{Store: &Store{DB: f.db}, Log: logger.NewLogger()}, "")
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	r := httptest.NewRequest("POST", "/api/hub/workspace-admins", bytes.NewBufferString(b))
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set("X-Veridian-Hub-Signature", sign("", ts, b))
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, r)
	assert.Equal(t, 503, rec.Code)
	assert.Equal(t, "member", f.role(t, 2, ws))
}

func (f *fixture) list(t *testing.T, w string) (int, []Member) {
	t.Helper()
	rec := f.do("GET", "/api/hub/workspace-admins?workspace_id="+w, "")
	var out struct {
		Members []Member `json:"members"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Members
}

func find(ms []Member, hub string) *Member {
	for i := range ms {
		if ms[i].HubUserID != nil && *ms[i].HubUserID == hub {
			return &ms[i]
		}
	}
	return nil
}

func TestGet_RelitLesRoles(t *testing.T) {
	f := newFixture(t)
	code, ms := f.list(t, ws)
	require.Equal(t, 200, code)
	require.NotNil(t, find(ms, hubOwner))
	assert.Equal(t, "owner", find(ms, hubOwner).Role)
	assert.False(t, find(ms, hubOwner).AdminGranted)
	assert.Nil(t, find(ms, hubKey), "aucune clé API")
	assert.Equal(t, "member", find(ms, hubMember).Role)

	f.post(ws, hubMember, "grant", false)
	f.post(ws, hubOwner, "grant", false)
	_, ms = f.list(t, ws)
	assert.Equal(t, "admin", find(ms, hubMember).Role)
	assert.True(t, find(ms, hubMember).AdminGranted)
	assert.False(t, find(ms, hubMember).AdminWithdrawn)
	assert.True(t, find(ms, hubOwner).AdminGranted, "la désignation d'un propriétaire est notée")

	// retiré de l'espace : la ligne reste, rôle vide, admin_withdrawn
	_, err := f.db.Exec(`DELETE FROM user_workspaces WHERE user_id=$1 AND workspace_id=$2`, uid(2), ws)
	require.NoError(t, err)
	_, ms = f.list(t, ws)
	m := find(ms, hubMember)
	require.NotNil(t, m)
	assert.Equal(t, "", m.Role)
	assert.True(t, m.AdminWithdrawn)
	assert.False(t, m.AdminGranted)

	// rétrogradé : retiré
	f.post(ws, hubMember, "grant", true) // 404 : toujours pas membre
	_, err = f.db.Exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,$2,'member','{}',NOW(),NOW())`, uid(2), ws)
	require.NoError(t, err)
	_, ms = f.list(t, ws)
	assert.True(t, find(ms, hubMember).AdminWithdrawn, "ré-ajouté comme membre : toujours marqué retiré")

	assert.Equal(t, 404, func() int { c, _ := f.list(t, "wsparti"); return c }())
	assert.Equal(t, 404, func() int { c, _ := f.list(t, "nulpart"); return c }())
	assert.Equal(t, 400, func() int { c, _ := f.list(t, ""); return c }())
}
