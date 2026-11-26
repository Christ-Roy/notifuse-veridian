package tenantlink

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
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

func systemDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testpg.DSN(t)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	name := fmt.Sprintf("tenantlink_test_%d", time.Now().UnixNano())
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
	for _, ws := range []string{"alpha", "beta", "gamma", "parti"} {
		_, err = db.Exec(`INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ($1,$1,NOW(),NOW())`, ws)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO veridian_plan (workspace_id) VALUES ($1)`, ws)
		require.NoError(t, err)
	}
	return db
}

func linkOf(t *testing.T, db *sql.DB, ws string) string {
	t.Helper()
	var v sql.NullString
	require.NoError(t, db.QueryRow(`SELECT hub_tenant_id FROM veridian_plan WHERE workspace_id=$1`, ws).Scan(&v))
	return v.String
}

// Le cas du 26/11 (CRM, cas 48) : un espace neuf se relie, le rejeu ne change rien, un autre tenant est refusé sans écriture.
func TestLink_PoseRejoueRefuseLeRepointage(t *testing.T) {
	db := systemDB(t)
	s := &Store{DB: db}
	ctx := context.Background()

	changed, err := s.Link(ctx, "alpha", "t-alpha", false)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "t-alpha", linkOf(t, db, "alpha"))

	changed, err = s.Link(ctx, "alpha", "t-alpha", false)
	require.NoError(t, err)
	assert.False(t, changed, "rejeu idempotent")

	_, err = s.Link(ctx, "alpha", "t-autre", false)
	var conflict *domain.HubTenantConflict
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "t-alpha", conflict.LinkedTenant)
	assert.Equal(t, "t-alpha", linkOf(t, db, "alpha"), "rien n'est écrit")

	changed, err = s.Link(ctx, "alpha", "t-autre", true)
	require.NoError(t, err)
	assert.True(t, changed, "replace explicite re-pointe")
	assert.Equal(t, "t-autre", linkOf(t, db, "alpha"))
}

// Un tenant ne se pose jamais sur deux espaces vivants, même avec replace ; un espace supprimé en douceur ne le retient pas.
func TestLink_UnTenantUnSeulEspaceVivant(t *testing.T) {
	db := systemDB(t)
	s := &Store{DB: db}
	ctx := context.Background()
	_, err := s.Link(ctx, "alpha", "t-x", false)
	require.NoError(t, err)

	for _, replace := range []bool{false, true} {
		_, err = s.Link(ctx, "beta", "t-x", replace)
		var conflict *domain.HubTenantConflict
		require.ErrorAs(t, err, &conflict, "replace=%v", replace)
		assert.Equal(t, "alpha", conflict.LinkedWorkspace)
		assert.Equal(t, "", linkOf(t, db, "beta"))
	}

	_, err = s.Link(ctx, "parti", "t-y", false)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE veridian_plan SET deleted_at=NOW() WHERE workspace_id='parti'`)
	require.NoError(t, err)
	changed, err := s.Link(ctx, "gamma", "t-y", false)
	require.NoError(t, err)
	assert.True(t, changed)

	_, err = s.Link(ctx, "inconnu", "t-z", false)
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

// Deux liens concurrents du même tenant vers deux espaces : un seul gagne (verrou sur le tenant).
func TestLink_ConcurrentsUnSeulGagne(t *testing.T) {
	db := systemDB(t)
	s := &Store{DB: db}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, ws := range []string{"alpha", "beta"} {
		wg.Add(1)
		go func(i int, ws string) {
			defer wg.Done()
			_, errs[i] = s.Link(context.Background(), ws, "t-course", false)
		}(i, ws)
	}
	wg.Wait()
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM veridian_plan WHERE hub_tenant_id='t-course'`).Scan(&n))
	assert.Equal(t, 1, n)
	assert.True(t, (errs[0] == nil) != (errs[1] == nil), "un succès, un conflit : %v", errs)
}

type fakeStore struct {
	changed bool
	err     error
	got     Request
}

func (f *fakeStore) Link(_ context.Context, ws, tenant string, replace bool) (bool, error) {
	f.got = Request{TenantID: ws, HubTenantID: tenant, Replace: replace}
	return f.changed, f.err
}

const secret = "secret-de-test"

func signed(t *testing.T, body string) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + body))
	r := httptest.NewRequest(http.MethodPost, "/api/hub/tenants/link", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set("X-Veridian-Hub-Signature", hex.EncodeToString(mac.Sum(nil)))
	return r
}

func serve(t *testing.T, st *fakeStore, r *http.Request) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, &Handler{Store: st}, secret)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestHandler_Contrat(t *testing.T) {
	st := &fakeStore{changed: true}
	code, body := serve(t, st, signed(t, `{"tenant_id":" alpha ","hub_tenant_id":"3108142a-4578-4604-87dd-2ecee740987b"}`))
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, body["changed"])
	assert.Equal(t, Request{TenantID: "alpha", HubTenantID: "3108142a-4578-4604-87dd-2ecee740987b"}, st.got)

	code, body = serve(t, &fakeStore{}, signed(t, `{"tenant_id":"alpha"}`))
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "invalid_payload", body["code"])

	code, _ = serve(t, &fakeStore{}, signed(t, `{"tenant_id":"alpha","hub_tenant_id":"x;drop"}`))
	assert.Equal(t, http.StatusBadRequest, code)

	code, body = serve(t, &fakeStore{err: sql.ErrNoRows}, signed(t, `{"tenant_id":"absent","hub_tenant_id":"t"}`))
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "tenant_not_found", body["code"])

	conflict := &domain.HubTenantConflict{WorkspaceID: "beta", HubTenantID: "t", LinkedWorkspace: "alpha"}
	code, body = serve(t, &fakeStore{err: conflict}, signed(t, `{"tenant_id":"beta","hub_tenant_id":"t"}`))
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "hub_tenant_conflict", body["code"])
	assert.Equal(t, "alpha", body["details"].(map[string]any)["linked_tenant_id"])

	code, _ = serve(t, &fakeStore{err: errors.New("base coupée")}, signed(t, `{"tenant_id":"alpha","hub_tenant_id":"t"}`))
	assert.Equal(t, http.StatusInternalServerError, code)
}

// Sans signature du Hub, rien n'atteint le store.
func TestHandler_SansSignatureRefuse(t *testing.T) {
	st := &fakeStore{}
	r := httptest.NewRequest(http.MethodPost, "/api/hub/tenants/link", bytes.NewBufferString(`{"tenant_id":"alpha","hub_tenant_id":"t"}`))
	code, _ := serve(t, st, r)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, Request{}, st.got)
}
