package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/folders"
	"github.com/Notifuse/notifuse/internal/veridian/folders/foldertest"
)

func token(t *testing.T) string {
	claims := &service.UserClaims{UserID: "u1", Type: "api_key", SessionID: "s1"}
	claims.RegisteredClaims = jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("sec"))
	require.NoError(t, err)
	return tok
}

type api struct {
	t   *testing.T
	mux *http.ServeMux
}

func newAPI(t *testing.T, db *sql.DB, authz folders.Authorizer) *api {
	svc := &folders.Service{DB: func(context.Context, string) (*sql.DB, error) { return db, nil }, Authz: authz}
	mux := http.NewServeMux()
	(&Handler{Svc: svc, GetJWTSecret: func() ([]byte, error) { return []byte("sec"), nil }}).RegisterRoutes(mux)
	return &api{t: t, mux: mux}
}

func (a *api) do(method, path, body string) (int, map[string]any) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(a.t))
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func allowAll(context.Context, string, folders.Kind, bool) error { return nil }

func TestHandler_ParcoursComplet(t *testing.T) {
	db := foldertest.NewDB(t)
	require.NoError(t, folders.EnsureSchema(context.Background(), db))
	_, err := db.Exec(`INSERT INTO segments (id, name) VALUES ('s1','Vieux / P1'), ('s2','P2')`)
	require.NoError(t, err)
	a := newAPI(t, db, allowAll)

	code, out := a.do("GET", "/api/veridian/folders.list?workspace_id=ws&kind=segments", "")
	require.Equal(t, 200, code)
	assert.Len(t, out["folders"], 1)

	code, out = a.do("POST", "/api/veridian/folders.create", `{"workspace_id":"ws","kind":"segments","path":"Cibles / Animaleries"}`)
	require.Equal(t, 201, code)
	folder := out["folder"].(map[string]any)
	assert.Equal(t, "Cibles / Animaleries", folder["path_text"])
	animaleries := folder["id"].(string)

	code, out = a.do("POST", "/api/veridian/segments.move", `{"workspace_id":"ws","ids":["s1","s2"],"folder_id":"`+animaleries+`"}`)
	require.Equal(t, 200, code, out)
	moved := out["moved"].([]any)
	assert.Equal(t, "Cibles / Animaleries / P1", moved[0].(map[string]any)["name"])
	assert.Equal(t, "Cibles / Animaleries / P2", moved[1].(map[string]any)["name"])

	code, out = a.do("POST", "/api/veridian/segments.move", `{"workspace_id":"ws","id":"s2","folder_path":"Autre / Sous","name":"Deux"}`)
	require.Equal(t, 200, code, out)
	assert.Equal(t, "Autre / Sous / Deux", names3(t, db, "segments")["s2"])

	code, out = a.do("POST", "/api/veridian/folders.rename", `{"workspace_id":"ws","id":"`+animaleries+`","name":"Animaux"}`)
	require.Equal(t, 200, code, out)
	assert.Equal(t, "Cibles / Animaux / P1", names3(t, db, "segments")["s1"])

	code, out = a.do("POST", "/api/veridian/folders.delete", `{"workspace_id":"ws","id":"`+animaleries+`"}`)
	assert.Equal(t, 409, code, "dossier non vide")
	assert.Contains(t, out["error"], "pas vide")
	code, _ = a.do("POST", "/api/veridian/folders.delete", `{"workspace_id":"ws","id":"`+animaleries+`","contents":"to_parent"}`)
	assert.Equal(t, 200, code)
	assert.Equal(t, "Cibles / P1", names3(t, db, "segments")["s1"])

	code, out = a.do("POST", "/api/veridian/folders.move", `{"workspace_id":"ws","id":"inconnu"}`)
	assert.Equal(t, 404, code, out)
	code, out = a.do("POST", "/api/veridian/folders.create", `{"workspace_id":"ws","kind":"segments","name":"A / B"}`)
	assert.Equal(t, 400, code, out)
	code, _ = a.do("POST", "/api/veridian/folders.create", `{"workspace_id":"ws","kind":"zzz","name":"A"}`)
	assert.Equal(t, 400, code)
	code, _ = a.do("POST", "/api/veridian/folders.create", `not json`)
	assert.Equal(t, 400, code)
	code, _ = a.do("POST", "/api/veridian/folders.delete", `{"workspace_id":"ws","id":"x","contents":"zap"}`)
	assert.Equal(t, 400, code)
}

func TestHandler_SansJetonOuSansDroit(t *testing.T) {
	db := foldertest.NewDB(t)
	require.NoError(t, folders.EnsureSchema(context.Background(), db))
	denied := func(context.Context, string, folders.Kind, bool) error {
		return &domain.ErrUnauthorized{Message: "not authorized for this workspace"}
	}
	a := newAPI(t, db, denied)

	req := httptest.NewRequest("GET", "/api/veridian/folders.list?workspace_id=ws&kind=lists", nil)
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	assert.Equal(t, 401, rec.Code, "sans jeton")

	code, _ := a.do("GET", "/api/veridian/folders.list?workspace_id=ws&kind=lists", "")
	assert.Equal(t, 403, code, "autre workspace : refuse, jamais un 500")
	code, _ = a.do("POST", "/api/veridian/lists.move", `{"workspace_id":"ws","id":"x","folder_id":""}`)
	assert.Equal(t, 403, code)
	code, _ = a.do("POST", "/api/veridian/folders.rename", `{"workspace_id":"ws","id":"x","name":"y"}`)
	assert.Equal(t, 403, code)
}

func TestHandler_ToutesLesRoutesDeclareesSontMontees(t *testing.T) {
	mux := http.NewServeMux()
	(&Handler{Svc: &folders.Service{}, GetJWTSecret: func() ([]byte, error) { return []byte("sec"), nil }}).RegisterRoutes(mux)
	for _, route := range Routes {
		method, path, _ := strings.Cut(route, " ")
		_, pattern := mux.Handler(httptest.NewRequest(method, path, nil))
		assert.Equal(t, route, pattern)
	}
}

// Toute route exposee est documentee dans l'OpenAPI groupee, avec sa methode : une route
// ajoutee sans doc (ou une doc d'une route retiree) fait echouer ce test.
func TestHandler_OpenAPIDocumenteToutesLesRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../../../openapi.json")
	require.NoError(t, err)
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	documented := 0
	for _, route := range Routes {
		method, path, _ := strings.Cut(route, " ")
		ops, ok := doc.Paths[path]
		require.True(t, ok, "route non documentee : %s", path)
		_, ok = ops[strings.ToLower(method)]
		assert.True(t, ok, "methode non documentee : %s", route)
		documented++
	}
	assert.Equal(t, len(Routes), documented)
	for path := range doc.Paths {
		if strings.HasPrefix(path, "/api/veridian/folders.") {
			found := false
			for _, route := range Routes {
				if strings.HasSuffix(route, " "+path) {
					found = true
				}
			}
			assert.True(t, found, "documentee mais non montee : %s", path)
		}
	}
}

func names3(t *testing.T, db *sql.DB, table string) map[string]string {
	rows, err := db.Query(`SELECT id, name FROM ` + table + ` ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		require.NoError(t, rows.Scan(&id, &name))
		out[id] = name
	}
	return out
}
