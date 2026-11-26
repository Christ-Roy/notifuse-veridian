package platformadmin

import (
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
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	admin   = "0a000000-0000-4000-8000-000000000001" // nous
	client  = "0a000000-0000-4000-8000-000000000002" // propriétaire du client
	member  = "0a000000-0000-4000-8000-000000000003" // membre du client
	machine = "0a000000-0000-4000-8000-000000000004"
	hubA    = "53245ae1-0000-4000-8000-000000000001"
	secret  = "test-hub-secret-32chars-min-len-ok"
)

func fixture(t *testing.T, adminRole string) (*Store, *sql.DB) {
	t.Helper()
	db := testpg.System(t, func(db *sql.DB) error {
		if err := database.InitializeDatabase(db, ""); err != nil {
			return err
		}
		_, err := veridianmig.Default.ApplySystem(context.Background(), db)
		return err
	})
	exec(t, db, `INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ('ws','ws',NOW(),NOW()),('autre','autre',NOW(),NOW())`)
	for id, typ := range map[string]string{admin: "user", client: "user", member: "user", machine: "api_key"} {
		exec(t, db, `INSERT INTO users (id,type,email,created_at,updated_at) VALUES ($1,$2,$3,NOW(),NOW())`, id, typ, id+"@t.fr")
	}
	exec(t, db, `UPDATE users SET hub_user_id=$1 WHERE id=$2`, hubA, admin)
	for id, role := range map[string]string{client: "owner", member: "member", machine: "owner"} {
		exec(t, db, `INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,'ws',$2,'{"contacts":{"read":true}}','2026-01-01','2026-01-02')`, id, role)
	}
	exec(t, db, `INSERT INTO user_workspaces (user_id,workspace_id,role,created_at,updated_at) VALUES ($1,'autre','owner','2026-01-01','2026-01-02')`, client)
	if adminRole != "" {
		exec(t, db, `INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,'ws',$2,'{}','2026-01-01','2026-01-02')`, admin, adminRole)
	}
	return &Store{DB: db}, db
}

func exec(t *testing.T, db *sql.DB, q string, a ...any) {
	t.Helper()
	if _, err := db.Exec(q, a...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// snapshot : toutes les appartenances de tous les espaces, sauf celle de l'administrateur dans ws.
func snapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT string_agg(row_to_json(uw)::text, E'\n' ORDER BY workspace_id, user_id) FROM user_workspaces uw WHERE NOT (user_id=$1 AND workspace_id='ws')`, admin).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func roleOf(t *testing.T, db *sql.DB, user string) string {
	t.Helper()
	var r string
	err := db.QueryRow(`SELECT role FROM user_workspaces WHERE user_id=$1 AND workspace_id='ws'`, user).Scan(&r)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return r
}

var grant = Grant{WorkspaceID: "ws", HubUserID: hubA, By: "opérateur", Reason: "essai jetable"}

func TestAdd_EnPlusDuClient_RienDAutreNeBouge(t *testing.T) {
	for role, state := range map[string]string{"": "added", "member": "promoted"} {
		t.Run(state, func(t *testing.T) {
			s, db := fixture(t, role)
			before := snapshot(t, db)
			dry := grant
			dry.DryRun = true
			if r, err := s.Add(context.Background(), dry); err != nil || r.State != state || roleOf(t, db, admin) != role {
				t.Fatalf("à blanc : %+v %v, rôle %q", r, err, roleOf(t, db, admin))
			}
			r, err := s.Add(context.Background(), grant)
			if err != nil || r.State != state || r.PreviousRole != role {
				t.Fatalf("%+v %v", r, err)
			}
			if roleOf(t, db, admin) != "owner" || roleOf(t, db, client) != "owner" {
				t.Fatal("deux propriétaires attendus")
			}
			if after := snapshot(t, db); after != before {
				t.Fatalf("une autre appartenance a bougé\navant %s\naprès %s", before, after)
			}
			r, err = s.Add(context.Background(), grant)
			if err != nil || r.State != "already_admin" || snapshot(t, db) != before {
				t.Fatalf("rejeu : %+v %v", r, err)
			}
			var by, reason, prev string
			if err := db.QueryRow(`SELECT granted_by, reason, previous_role FROM veridian_platform_admin`).Scan(&by, &reason, &prev); err != nil || by != grant.By || reason != grant.Reason || prev != role {
				t.Fatalf("journal : %q %q %q %v", by, reason, prev, err)
			}
			if role == "member" {
				var upd time.Time
				_ = db.QueryRow(`SELECT updated_at FROM user_workspaces WHERE user_id=$1 AND workspace_id='ws'`, admin).Scan(&upd)
				if upd.Format("2006-01-02") != "2026-01-02" {
					t.Fatalf("la date de l'appartenance a bougé : %v", upd)
				}
			}
		})
	}
}

func TestAdd_Refus(t *testing.T) {
	ctx := context.Background()
	s, db := fixture(t, "member")
	before := snapshot(t, db)
	bad := func(name string, g Grant, want error) {
		t.Helper()
		if _, err := s.Add(ctx, g); !errors.Is(err, want) {
			t.Fatalf("%s : %v, attendu %v", name, err, want)
		}
		if roleOf(t, db, admin) != "member" || snapshot(t, db) != before {
			t.Fatalf("%s : quelque chose a bougé", name)
		}
	}
	g := grant
	g.WorkspaceID = "inconnu"
	bad("espace inconnu", g, ErrWorkspaceUnknown)
	g = grant
	g.HubUserID = "53245ae1-0000-4000-8000-0000000000ff"
	bad("personne inconnue", g, ErrPersonUnknown)
	exec(t, db, `UPDATE users SET hub_deleted_at=NOW() WHERE id=$1`, admin)
	bad("compte supprimé au Hub", grant, ErrPersonUnknown)
	exec(t, db, `UPDATE users SET hub_deleted_at=NULL, type='api_key' WHERE id=$1`, admin)
	bad("compte machine", grant, ErrPersonUnknown)
	exec(t, db, `UPDATE users SET type='user' WHERE id=$1`, admin)
	exec(t, db, `UPDATE user_workspaces SET role='member' WHERE user_id=$1 AND workspace_id='ws'`, client)
	before = snapshot(t, db)
	bad("aucun propriétaire humain du client (la machine ne compte pas)", grant, ErrNoClientOwner)
}

func TestAdd_ProprietaireEnPropre_SansMarque(t *testing.T) {
	s, db := fixture(t, "owner")
	r, err := s.Add(context.Background(), grant)
	if err != nil || r.State != "already_owner" {
		t.Fatalf("%+v %v", r, err)
	}
	var n int
	if _ = db.QueryRow(`SELECT count(*) FROM veridian_platform_admin`).Scan(&n); n != 0 {
		t.Fatal("un propriétaire en propre n'est pas marqué")
	}
}

func TestAdd_RetireOuRetrograde_JamaisRemis(t *testing.T) {
	ctx := context.Background()
	for name, q := range map[string]string{
		"retiré sans trace": `DELETE FROM user_workspaces WHERE user_id=$1 AND workspace_id='ws'`,
		"rétrogradé":        `UPDATE user_workspaces SET role='member' WHERE user_id=$1 AND workspace_id='ws'`,
	} {
		s, db := fixture(t, "member")
		if _, err := s.Add(ctx, grant); err != nil {
			t.Fatal(err)
		}
		exec(t, db, q, admin)
		was := roleOf(t, db, admin)
		if _, err := s.Add(ctx, grant); !errors.Is(err, ErrRemovedByOwner) || roleOf(t, db, admin) != was {
			t.Fatalf("%s : %v", name, err)
		}
	}
}

func TestOnlyThisOneChanged(t *testing.T) {
	me, them := Member{UserID: "a", Role: "owner", PlatformAdmin: true}, Member{UserID: "b", Role: "owner", fingerprint: "x"}
	if err := onlyThisOneChanged([]Member{them}, []Member{me, them}, "a"); err != nil {
		t.Fatal(err)
	}
	moved := them
	moved.fingerprint = "y"
	for name, after := range map[string][]Member{"modifiée": {me, moved}, "disparue": {me}, "apparue": {me, them, {UserID: "c"}}, "pas posée": {them}} {
		if onlyThisOneChanged([]Member{them}, after, "a") == nil {
			t.Errorf("%s : doit être vu", name)
		}
	}
}

func signed(method, path, body string) *http.Request {
	ts := fmt.Sprint(time.Now().UnixMilli())
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "." + body))
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set("X-Veridian-Hub-Signature", hex.EncodeToString(m.Sum(nil)))
	return r
}

func TestRoute_SigneeParHubUserIDSeulement(t *testing.T) {
	s, db := fixture(t, "member")
	mux := http.NewServeMux()
	Mount(mux, s, secret, logger.NewLogger())
	do := func(r *http.Request) (int, map[string]any) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	ok := `{"workspace_id":"ws","hub_user_id":"` + hubA + `","granted_by":"opérateur","reason":"essai jetable"}`
	if c, _ := do(httptest.NewRequest("POST", "/api/veridian/admin/platform-admin", strings.NewReader(ok))); c != 401 {
		t.Fatalf("sans signature : %d", c)
	}
	for name, body := range map[string]string{
		"adresse":       `{"workspace_id":"ws","email":"a@b.fr","granted_by":"opérateur","reason":"essai jetable"}`,
		"pas un uuid":   `{"workspace_id":"ws","hub_user_id":"a@b.fr","granted_by":"opérateur","reason":"essai jetable"}`,
		"sans raison":   `{"workspace_id":"ws","hub_user_id":"` + hubA + `","granted_by":"opérateur"}`,
		"sans auteur":   `{"workspace_id":"ws","hub_user_id":"` + hubA + `","reason":"essai jetable"}`,
		"sans espace":   `{"hub_user_id":"` + hubA + `","granted_by":"opérateur","reason":"essai jetable"}`,
		"pas du JSON":   `{`,
		"champ étrange": `{"workspace_id":"ws","hub_user_id":"` + hubA + `","granted_by":"op","reason":"essai jetable","role":"owner"}`,
	} {
		if c, m := do(signed("POST", "/api/veridian/admin/platform-admin", body)); c != 400 || m["code"] != "invalid_payload" {
			t.Errorf("%s : %d %v", name, c, m)
		}
	}
	if roleOf(t, db, admin) != "member" {
		t.Fatal("un refus a écrit")
	}
	if c, m := do(signed("POST", "/api/veridian/admin/platform-admin", ok)); c != 200 || m["state"] != "promoted" {
		t.Fatalf("%d %v", c, m)
	}
	c, m := do(signed("GET", "/api/veridian/admin/platform-admin?workspace_id=ws", ""))
	if c != 200 || len(m["members"].([]any)) != 4 {
		t.Fatalf("relevé : %d %v", c, m)
	}
	if b, _ := json.Marshal(m); strings.Contains(string(b), "@") {
		t.Fatal("une adresse sort du relevé")
	}
}

func TestGuard_LeClientRetire_LInverseEstRefuse(t *testing.T) {
	s, db := fixture(t, "member")
	if _, err := s.Add(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	mux, calls, status := http.NewServeMux(), 0, 200
	for _, p := range GuardedRoutes {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			calls++
			var in struct {
				UserID string `json:"user_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("l'amont ne relit pas le corps : %v", err)
			}
			if status == 200 && strings.HasSuffix(p, "removeMember") {
				exec(t, db, `DELETE FROM user_workspaces WHERE user_id=$1 AND workspace_id='ws'`, in.UserID)
			}
			w.WriteHeader(status)
		})
	}
	auth := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), domain.UserIDKey, r.Header.Get("X-User"))))
		})
	}
	if err := Guard(mux, s, auth, logger.NewLogger()); err != nil {
		t.Fatal(err)
	}
	do := func(path, who, target string) int {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"workspace_id":"ws","user_id":"`+target+`"}`))
		r.Header.Set("X-User", who)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	for _, p := range GuardedRoutes {
		if c := do(p, admin, client); c != 403 || calls != 0 {
			t.Fatalf("%s : l'administrateur touche au propriétaire du client : %d, amont appelé %d fois", p, c, calls)
		}
	}
	if c := do(GuardedRoutes[1], admin, member); c != 200 {
		t.Fatalf("un propriétaire gère les membres : %d", c)
	}
	status = 500
	if do(GuardedRoutes[0], client, admin); roleOf(t, db, admin) != "owner" {
		t.Fatal("échec de l'amont")
	}
	var removed sql.NullTime
	if _ = db.QueryRow(`SELECT removed_at FROM veridian_platform_admin`).Scan(&removed); removed.Valid {
		t.Fatal("un échec n'est pas un retrait")
	}
	status = 200
	if c := do(GuardedRoutes[0], client, admin); c != 200 || roleOf(t, db, admin) != "" {
		t.Fatalf("le client retire : %d", c)
	}
	var by string
	if err := db.QueryRow(`SELECT removed_by::text FROM veridian_platform_admin WHERE removed_at IS NOT NULL`).Scan(&by); err != nil || by != client {
		t.Fatalf("trace : %q %v", by, err)
	}
	if _, err := s.Add(context.Background(), grant); !errors.Is(err, ErrRemovedByOwner) {
		t.Fatalf("remis tout seul : %v", err)
	}
	if gone, err := s.Gone(context.Background(), "ws", hubA); err != nil || !gone {
		t.Fatalf("parti : %v %v", gone, err)
	}
	if err := Guard(http.NewServeMux(), s, auth, logger.NewLogger()); err == nil {
		t.Fatal("route amont absente : le démarrage doit le dire")
	}
}
