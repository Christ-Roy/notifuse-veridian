package actions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

func systemDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := testpg.DSN(t)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	name := fmt.Sprintf("actions_test_%d", time.Now().UnixNano())
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
	for i := 0; i < 2; i++ {
		_, err = veridianmig.Default.ApplySystem(context.Background(), db)
		require.NoError(t, err)
	}
	return db
}

func TestSQLStore_JTI_UneFois_MemeEnParallele(t *testing.T) {
	db := systemDB(t)
	s := &SQLStore{DB: db}
	var won int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := s.ConsumeJTI(context.Background(), "jeton-1"); err == nil && ok {
				atomic.AddInt32(&won, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, won)
	// Encore connu à 9 minutes, oublié après 10 : le jeton n'a vécu que 120 s.
	_, err := db.Exec(`UPDATE veridian_action_jti SET seen_at = NOW() - INTERVAL '9 minutes'`)
	require.NoError(t, err)
	ok, err := s.ConsumeJTI(context.Background(), "jeton-1")
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = db.Exec(`UPDATE veridian_action_jti SET seen_at = NOW() - INTERVAL '11 minutes'`)
	require.NoError(t, err)
	_, err = s.ConsumeJTI(context.Background(), "jeton-2")
	require.NoError(t, err)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_action_jti`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestSQLStore_Resolve(t *testing.T) {
	db := systemDB(t)
	s := &SQLStore{DB: db}
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.Exec(q, args...)
		require.NoError(t, err, q)
	}
	const uid = "00000000-0000-0000-0000-000000000001"
	exec(`INSERT INTO users (id,type,email,hub_user_id,created_at,updated_at) VALUES ($1,'user','u@x.test',$2,NOW(),NOW())`, uid, hubUser)
	for ws, tn := range map[string]string{"alpha": "t-alpha", "beta": "t-beta", "sommeil": "t-sommeil", "parti": "t-parti"} {
		exec(`INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ($1,$1,NOW(),NOW())`, ws)
		exec(`INSERT INTO veridian_plan (workspace_id,hub_tenant_id) VALUES ($1,$2)`, ws, tn)
	}
	for _, ws := range []string{"alpha", "sommeil", "parti"} {
		exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,$2,'member','{"contacts":{"read":true,"write":true}}',NOW(),NOW())`, uid, ws)
	}
	exec(`UPDATE veridian_plan SET status='suspended' WHERE workspace_id='sommeil'`)
	exec(`UPDATE veridian_plan SET deleted_at=NOW() WHERE workspace_id='parti'`)

	m, err := s.Resolve(ctx, hubUser, "t-alpha")
	require.NoError(t, err)
	assert.Equal(t, "alpha", m.Workspace.WorkspaceID)
	assert.Equal(t, "u@x.test", m.Email)
	assert.True(t, m.Workspace.Permissions["contacts"].Write)

	for tn, want := range map[string]error{"t-beta": ErrNotMember, "t-sommeil": ErrSuspended, "t-parti": ErrUnlinkedTenant, "inconnu": ErrUnlinkedTenant,
		"alpha": ErrUnlinkedTenant /* le nom de l'espace n'est pas un tenant */, "": ErrUnlinkedTenant} {
		_, err := s.Resolve(ctx, hubUser, tn)
		assert.ErrorIs(t, err, want, tn)
	}
	_, err = s.Resolve(ctx, "99999999-2222-3333-4444-555555555555", "t-alpha")
	assert.ErrorIs(t, err, ErrUnknownUser)
	_, err = s.Resolve(ctx, "pas un uuid", "t-alpha")
	assert.ErrorIs(t, err, ErrUnknownUser)

	exec(`INSERT INTO veridian_frozen_members (workspace_id,user_id,reason) VALUES ('alpha',$1,'x')`, uid)
	_, err = s.Resolve(ctx, hubUser, "t-alpha")
	assert.ErrorIs(t, err, ErrSuspended, "membre gelé")
	exec(`DELETE FROM veridian_frozen_members`)
	exec(`UPDATE users SET hub_deleted_at=NOW()`)
	_, err = s.Resolve(ctx, hubUser, "t-alpha")
	assert.ErrorIs(t, err, ErrUnknownUser, "compte supprimé au Hub")
}

func TestSQLStore_Idempotence(t *testing.T) {
	db := systemDB(t)
	s := &SQLStore{DB: db}
	ctx := context.Background()
	o := Origin{App: "app:crm", JTI: "jeton-00001", HubUserID: hubUser}
	begin := func(key, hash string) (json.RawMessage, error) { return s.Begin(ctx, "ws1", key, "a", hash, o) }

	prev, err := begin("cle1", "h1")
	require.NoError(t, err)
	assert.Nil(t, prev)
	_, err = begin("cle1", "h1")
	assert.ErrorIs(t, err, ErrInProgress)
	_, err = begin("cle1", "h2")
	assert.ErrorIs(t, err, ErrKeyReused)

	require.NoError(t, s.Finish(ctx, "ws1", "app:crm", "cle1", json.RawMessage(`{"created":25}`)))
	prev, err = begin("cle1", "h1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"created":25}`, string(prev))
	require.NoError(t, s.Release(ctx, "ws1", "app:crm", "cle1"), "une clé faite ne se rend pas")
	prev, err = begin("cle1", "h1")
	require.NoError(t, err)
	assert.NotNil(t, prev)

	// Une autre app, ou un autre espace, a ses propres clés.
	other := o
	other.App = "app:prospection"
	p2, err := s.Begin(ctx, "ws1", "cle1", "a", "h9", other)
	require.NoError(t, err)
	assert.Nil(t, p2)
	p3, err := s.Begin(ctx, "ws2", "cle1", "a", "h9", o)
	require.NoError(t, err)
	assert.Nil(t, p3)

	// Rendue, la clé ressert ; un geste mort depuis 2 minutes est repris, pas avant, et pas pour une autre demande.
	_, err = begin("cle2", "h1")
	require.NoError(t, err)
	require.NoError(t, s.Release(ctx, "ws1", "app:crm", "cle2"))
	_, err = begin("cle2", "h1")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE veridian_action_done SET created_at = NOW() - INTERVAL '3 minutes' WHERE idem_key='cle2'`)
	require.NoError(t, err)
	_, err = begin("cle2", "h2")
	assert.ErrorIs(t, err, ErrKeyReused)
	prev, err = begin("cle2", "h1")
	require.NoError(t, err)
	assert.Nil(t, prev)

	// 30 jours de mémoire.
	_, err = db.Exec(`UPDATE veridian_action_done SET created_at = NOW() - INTERVAL '29 days' WHERE idem_key='cle1'`)
	require.NoError(t, err)
	prev, err = begin("cle1", "h1")
	require.NoError(t, err)
	assert.NotNil(t, prev)
	_, err = db.Exec(`UPDATE veridian_action_done SET created_at = NOW() - INTERVAL '31 days' WHERE idem_key='cle1'`)
	require.NoError(t, err)
	prev, err = begin("cle1", "h1")
	require.NoError(t, err)
	assert.Nil(t, prev)
}
