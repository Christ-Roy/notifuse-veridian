package hubusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

// Base système réelle (schéma et migrations Veridian), jetée à la fin.
func systemDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testpg.DSN(t)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	name := fmt.Sprintf("hubusers_test_%d", time.Now().UnixNano())
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
	return db
}

func seedMembers(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(q string, args ...interface{}) {
		_, err := db.Exec(q, args...)
		require.NoError(t, err, q)
	}
	for _, ws := range []string{"alpha", "beta", "parti", "sansplan", "robots"} {
		exec(`INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ($1,$1,NOW(),NOW())`, ws)
		if ws != "sansplan" {
			exec(`INSERT INTO veridian_plan (workspace_id) VALUES ($1)`, ws)
		}
	}
	exec(`UPDATE veridian_plan SET hub_tenant_id='tenant-alpha' WHERE workspace_id='alpha'`)
	exec(`UPDATE veridian_plan SET deleted_at=NOW() WHERE workspace_id='parti'`)
	user := func(n int, kind, email string, hub interface{}) string {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
		exec(`INSERT INTO users (id,type,email,name,hub_user_id,created_at,updated_at) VALUES ($1,$2,$3,'Nom Secret',$4,NOW(),NOW())`, id, kind, email, hub)
		return id
	}
	join := func(uid, ws, role string) {
		exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,created_at,updated_at) VALUES ($1,$2,$3,NOW(),NOW())`, uid, ws, role)
	}
	relie := user(1, "user", "relie@x.test", hubID)
	libre := user(2, "user", "libre@x.test", nil)
	coupe := user(3, "user", "coupe@x.test", nil)
	cle := user(4, "api_key", "cle@x.test", nil)
	user(5, "user", "sansespace@x.test", nil)
	exec(`UPDATE users SET hub_deleted_at=NOW() WHERE id=$1`, coupe)
	join(relie, "alpha", "owner")
	join(libre, "alpha", "member")
	join(coupe, "beta", "member")
	join(relie, "beta", "member")
	join(cle, "alpha", "member")
	join(cle, "robots", "owner")
	join(relie, "parti", "owner")
	join(libre, "sansplan", "owner")
}

func TestSQLStore_ListMembers_VraieBase(t *testing.T) {
	db := systemDB(t)
	seedMembers(t, db)
	s := &SQLStore{DB: db}

	got, err := s.ListMembers(context.Background(), "")
	require.NoError(t, err)
	lines := make([]string, 0, len(got))
	for _, m := range got {
		tenant, hub := "-", "-"
		if m.HubTenantID != nil {
			tenant = *m.HubTenantID
		}
		if m.HubUserID != nil {
			hub = "lié"
			assert.Equal(t, hubID, *m.HubUserID)
		}
		lines = append(lines, m.WorkspaceID+" "+tenant+" "+hub+" "+m.Email+" "+m.Role)
	}
	assert.Equal(t, []string{
		"alpha tenant-alpha - libre@x.test member",
		"alpha tenant-alpha lié relie@x.test owner",
		"beta - - coupe@x.test member", // supprimé au Hub, encore membre : l'écart à voir
		"beta - lié relie@x.test member",
		"sansplan - - libre@x.test owner",
	}, lines, "ni clé API, ni espace supprimé, ni humain sans espace")

	one, err := s.ListMembers(context.Background(), "beta")
	require.NoError(t, err)
	assert.Len(t, one, 2)

	none, err := s.ListMembers(context.Background(), "robots")
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none, "un espace tenu par une seule clé API n'a aucun humain")

	for ws, want := range map[string]bool{"alpha": true, "sansplan": true, "robots": true, "parti": false, "inconnu": false} {
		live, err := s.WorkspaceLive(context.Background(), ws)
		require.NoError(t, err)
		assert.Equal(t, want, live, ws)
	}
}

func TestMembers_RienDAutreQueLesCinqChamps(t *testing.T) {
	db := systemDB(t)
	seedMembers(t, db)
	m := http.NewServeMux()
	Mount(m, &Handler{Store: &SQLStore{DB: db}}, testSecret)

	rec := do(m, signed("GET", "/api/veridian/membres", "", ""))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Nom Secret")
	assert.NotContains(t, rec.Body.String(), "00000000-0000", "users.id n'est jamais exposé")

	var out map[string][]map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out, 1)
	require.Len(t, out["members"], 5)
	for _, row := range out["members"] {
		keys := make([]string, 0, len(row))
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		assert.Equal(t, []string{"email", "hub_tenant_id", "hub_user_id", "role", "workspace_id"}, keys)
	}
}

func TestMembers_EspaceInconnu_404_JamaisUneListeVide(t *testing.T) {
	f := &fakeStore{liveWS: map[string]bool{"alpha": true}}
	m := mux(f)

	rec := do(m, signed("GET", "/api/veridian/membres?workspace_id=alhpa", "", ""))
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":"workspace_not_found"}`, rec.Body.String())
	rec = do(m, signed("GET", "/api/veridian/membres?workspace_id=", "", ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code, "un filtre vide ne vaut pas « tous les espaces »")
	assert.Empty(t, f.membersFor)

	rec = do(m, signed("GET", "/api/veridian/membres?workspace_id=alpha", "", ""))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"members":[]}`, rec.Body.String())
	assert.Equal(t, []string{"alpha"}, f.membersFor)
}

func TestMembers_LectureEnEchec_500_SansListe(t *testing.T) {
	f := &fakeStore{membersErr: errors.New("base coupée")}
	rec := do(mux(f), signed("GET", "/api/veridian/membres", "", ""))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "members")
	assert.NotContains(t, rec.Body.String(), "base coupée")
}
