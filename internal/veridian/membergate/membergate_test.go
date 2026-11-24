package membergate

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Le mux porte les routes amont telles qu'elles sont enregistrées (sans méthode), avant et après le module.
func upstream(reached *[]string) *http.ServeMux {
	mux := http.NewServeMux()
	for _, p := range []string{"/api/workspaces.inviteMember", "/api/workspaces.acceptInvitation", "/api/workspaces.verifyInvitationToken",
		"/api/workspaces.removeMember", "/api/workspaces.createAPIKey", "/api/veridian/workspaces.inviteMember", "/"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { *reached = append(*reached, r.Method+" "+p) })
	}
	return mux
}

func call(mux http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
	return rec
}

func TestMount_FermeLesDeuxRoutes_RefusLisible(t *testing.T) {
	var reached []string
	mux := upstream(&reached)
	Mount(mux, true, "https://hub.exemple.test/", nil)

	for _, p := range ClosedRoutes {
		rec := call(mux, "POST", p)
		assert.Equal(t, http.StatusForbidden, rec.Code, p)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
		var out map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		assert.Equal(t, Code, out["code"])
		assert.Equal(t, "https://hub.exemple.test", out["hub_url"])
		assert.Contains(t, out["error"], "https://hub.exemple.test")
	}
	assert.Empty(t, reached, "l'amont n'est jamais atteint")
	assert.Equal(t, []string{"/api/workspaces.inviteMember", "/api/workspaces.acceptInvitation"}, ClosedRoutes)
}

func TestMount_LesAutresRoutesRestent(t *testing.T) {
	var reached []string
	mux := upstream(&reached)
	Mount(mux, true, "", nil)
	for _, p := range []string{"/api/workspaces.verifyInvitationToken", "/api/workspaces.removeMember", "/api/workspaces.createAPIKey", "/api/veridian/workspaces.inviteMember"} {
		assert.Equal(t, http.StatusOK, call(mux, "POST", p).Code, p)
	}
	assert.Len(t, reached, 4)
	// Une autre méthode va à l'amont, qui la refuse lui-même (405, testé dans internal/http).
	call(mux, "GET", "/api/workspaces.inviteMember")
	assert.Equal(t, "GET /api/workspaces.inviteMember", reached[4])
	assert.Contains(t, call(mux, "POST", ClosedRoutes[0]).Body.String(), defaultHubURL)
}

func TestMount_SansHub_ComportementAmontIntact(t *testing.T) {
	var reached []string
	mux := upstream(&reached)
	Mount(mux, false, "", nil)
	for _, p := range ClosedRoutes {
		assert.Equal(t, http.StatusOK, call(mux, "POST", p).Code)
	}
	assert.Len(t, reached, 2)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		require.NotEqual(t, up, dir, "go.mod introuvable")
		dir = up
	}
}

// La CLASSE : toute ligne de code qui crée un utilisateur, l'ajoute à un espace ou appelle les deux méthodes amont est connue et
// justifiée ici. Une porte neuve (la nôtre, ou une de l'amont à la prochaine synchronisation) fait échouer ce test.
func TestPortes_ToutAjoutDHumainEstConnu(t *testing.T) {
	known := map[string]string{
		"internal/service/setup_service.go CreateUser":                          "premier démarrage, refusé une fois l'application installée",
		"internal/service/veridian_co_owner.go CreateUser":                      "copropriétaire configuré (Robert)",
		"internal/service/veridian_co_owner.go AddUserToWorkspace":              "copropriétaire configuré (Robert)",
		"internal/service/veridian_membership_service.go CreateUser":            "route signée par le Hub",
		"internal/service/veridian_membership_service.go AddUserToWorkspace":    "route signée par le Hub",
		"internal/service/veridian_service.go CreateUser":                       "route signée par le Hub",
		"internal/service/veridian_service.go AddUserToWorkspace":               "route signée par le Hub",
		"internal/service/veridian_service.go TransferOwnership":                "route signée par le Hub",
		"internal/service/workspace_service.go CreateUser":                      "clé API (machine) et acceptInvitation (fermée)",
		"internal/service/workspace_service.go AddUserToWorkspace":              "création d'espace (Hub), inviteMember et acceptInvitation (fermées), clé API, transfert (Hub)",
		"internal/http/workspace_handler.go InviteMember":                       "POST fermé par ce module",
		"internal/http/workspace_handler.go AcceptInvitation":                   "POST fermé par ce module",
		"internal/repository/user_postgres.go INSERT INTO users":                "dépôt",
		"internal/repository/workspace_postgres.go INSERT INTO user_workspaces": "dépôt",
		"internal/database/init.go INSERT INTO users":                           "compte racine au premier démarrage",
	}
	root := repoRoot(t)
	files := 0
	found := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		f := filepath.ToSlash(rel)
		if strings.HasSuffix(f, "_test.go") || strings.Contains(f, "/mocks/") || strings.HasPrefix(f, "internal/veridian/membergate/") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		for _, line := range strings.Split(string(src), "\n") {
			code := strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
			for _, m := range []string{"CreateUser", "AddUserToWorkspace", "InviteMember", "AcceptInvitation", "TransferOwnership"} {
				if strings.Contains(code, "."+m+"(") {
					found[f+" "+m] = true
				}
			}
			for _, table := range []string{"users", "user_workspaces"} {
				low := strings.ToLower(code)
				if strings.Contains(low, "insert into "+table+" ") || strings.Contains(low, "insert into "+table+"(") {
					found[f+" INSERT INTO "+table] = true
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, files, 200, "le contrôle n'a pas lu le code")
	var unknown, gone []string
	for k := range found {
		if _, ok := known[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	for k := range known {
		if !found[k] {
			gone = append(gone, k)
		}
	}
	sort.Strings(unknown)
	sort.Strings(gone)
	assert.Empty(t, unknown, "porte neuve qui ajoute un humain : passer par le Hub, ou la justifier ici")
	assert.Empty(t, gone, "porte connue disparue (ou test devenu aveugle) : retirer la ligne")
}
