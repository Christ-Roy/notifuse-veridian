package mcp

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

func TestCapacite_McpEnregistree(t *testing.T) {
	var names []string
	for _, c := range platform.Default.List() {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "mcp")
	assert.Equal(t, "d", envOr("VERIDIAN_MCP_TEST_ABSENTE", "d"))
}

func TestMount_PoseLaRouteMcp(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, &Handler{Verifier: &Verifier{}})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/mcp", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestSQLStore_Resolve(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &SQLStore{DB: db}

	mock.ExpectQuery(`FROM users WHERE hub_user_id`).WithArgs("hub1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}).AddRow("u1", "u@x.test"))
	mock.ExpectQuery(`FROM user_workspaces uw`).WithArgs("u1", "").
		WillReturnRows(sqlmock.NewRows([]string{"workspace_id", "role", "permissions"}).
			AddRow("ws1", "member", []byte(`{"contacts":{"read":true,"write":false}}`)))
	m, err := s.Resolve(context.Background(), "hub1", "")
	require.NoError(t, err)
	assert.Equal(t, "ws1", m.Workspace.WorkspaceID)
	assert.True(t, m.Workspace.HasPermission(domain.PermissionResourceContacts, domain.PermissionTypeRead))
	assert.False(t, m.Workspace.HasPermission(domain.PermissionResourceContacts, domain.PermissionTypeWrite))

	mock.ExpectQuery(`FROM users WHERE hub_user_id`).WithArgs("inconnu").WillReturnError(sql.ErrNoRows)
	_, err = s.Resolve(context.Background(), "inconnu", "")
	assert.ErrorIs(t, err, ErrUnknownUser)

	mock.ExpectQuery(`FROM users WHERE hub_user_id`).WithArgs("hub2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}).AddRow("u2", "v@x.test"))
	mock.ExpectQuery(`FROM user_workspaces uw`).WithArgs("u2", "").WillReturnError(sql.ErrNoRows)
	_, err = s.Resolve(context.Background(), "hub2", "")
	assert.ErrorIs(t, err, ErrNoWorkspace)

	mock.ExpectQuery(`FROM users WHERE hub_user_id`).WithArgs("hub3").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}).AddRow("u3", "w@x.test"))
	mock.ExpectQuery(`FROM user_workspaces uw`).WithArgs("u3", "autre-espace").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM veridian_plan WHERE workspace_id = \$1 OR hub_tenant_id = \$1\)`).WithArgs("autre-espace").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	_, err = s.Resolve(context.Background(), "hub3", "autre-espace")
	assert.ErrorIs(t, err, ErrNotMember, "l'espace existe mais l'utilisateur n'en est pas membre")

	mock.ExpectQuery(`FROM users WHERE hub_user_id`).WithArgs("hub4").
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}).AddRow("u4", "x@x.test"))
	mock.ExpectQuery(`FROM user_workspaces uw.*p\.hub_tenant_id = \$2`).WithArgs("u4", "359b76d5-uuid-du-hub").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs("359b76d5-uuid-du-hub").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	_, err = s.Resolve(context.Background(), "hub4", "359b76d5-uuid-du-hub")
	assert.ErrorIs(t, err, ErrUnknownTenant, "aucun espace relie a cet id de tenant du Hub")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMuxDispatcher_RejoueAvecUneSessionCourteQuiDisparait(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	auth := service.NewAuthService(service.AuthServiceConfig{GetSecret: func() ([]byte, error) { return []byte("secret-de-test"), nil }})

	mux := http.NewServeMux()
	var gotAuth, gotWS, gotCT string
	mux.HandleFunc("/api/lists.list", func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotWS, gotCT = r.Header.Get("Authorization"), r.URL.Query().Get("workspace_id"), r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"lists":[]}`))
	})
	d := &MuxDispatcher{Mux: mux, Auth: auth, DB: db}

	mock.ExpectExec(`INSERT INTO user_sessions`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM user_sessions`).WillReturnResult(sqlmock.NewResult(0, 1))
	status, body, err := d.Do(context.Background(), &Member{UserID: "u1", Email: "u@x.test"}, http.MethodGet, "/api/lists.list",
		map[string][]string{"workspace_id": {"ws1"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, status)
	assert.JSONEq(t, `{"lists":[]}`, string(body))
	assert.Contains(t, gotAuth, "Bearer ")
	assert.Equal(t, "ws1", gotWS)
	assert.Empty(t, gotCT, "pas de corps : pas de Content-Type")
	require.NoError(t, mock.ExpectationsWereMet(), "la session est creee puis supprimee")

	mock.ExpectExec(`INSERT INTO user_sessions`).WillReturnError(sql.ErrConnDone)
	_, _, err = d.Do(context.Background(), &Member{UserID: "u1"}, http.MethodGet, "/api/lists.list", nil, nil)
	assert.Error(t, err)
}

func TestMuxDispatcher_CorpsJSON(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	auth := service.NewAuthService(service.AuthServiceConfig{GetSecret: func() ([]byte, error) { return []byte("secret-de-test"), nil }})
	mux := http.NewServeMux()
	var gotCT string
	mux.HandleFunc("/api/contacts.upsert", func(w http.ResponseWriter, r *http.Request) { gotCT = r.Header.Get("Content-Type") })
	mock.ExpectExec(`INSERT INTO user_sessions`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`DELETE FROM user_sessions`).WillReturnResult(sqlmock.NewResult(0, 1))
	status, _, err := (&MuxDispatcher{Mux: mux, Auth: auth, DB: db}).Do(context.Background(), &Member{UserID: "u1"}, http.MethodPost, "/api/contacts.upsert", nil, []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "application/json", gotCT)
}
