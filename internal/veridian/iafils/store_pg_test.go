package iafils

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

const (
	uApp     = "0a000000-0000-4000-8000-000000000001"
	uAutre   = "0a000000-0000-4000-8000-000000000002"
	hubAutre = "44444444-4444-4444-8444-444444444444"
)

func systemDB(t *testing.T) *sql.DB {
	return testpg.System(t, func(db *sql.DB) error {
		if err := database.InitializeDatabase(db, ""); err != nil {
			return err
		}
		for i := 0; i < 2; i++ { // rejouable : une migration additive ne casse pas au second passage
			if _, err := veridianmig.Default.ApplySystem(context.Background(), db); err != nil {
				return err
			}
		}
		return nil
	})
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(q, args...)
	require.NoError(t, err, q)
}

// monde : deux espaces reliés à deux organisations ; hubUser est membre de « alpha » seulement, hubAutre des deux.
func monde(t *testing.T) (*Store, *sql.DB) {
	db := systemDB(t)
	exec(t, db, `INSERT INTO users (id,type,email,hub_user_id,created_at,updated_at) VALUES ($1,'user','a@x.test',$2,NOW(),NOW())`, uApp, hubUser)
	exec(t, db, `INSERT INTO users (id,type,email,hub_user_id,created_at,updated_at) VALUES ($1,'user','b@x.test',$2,NOW(),NOW())`, uAutre, hubAutre)
	for ws, tn := range map[string]string{"alpha": hubTenant, "beta": autreOrg} {
		exec(t, db, `INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ($1,$1,NOW(),NOW())`, ws)
		exec(t, db, `INSERT INTO veridian_plan (workspace_id,hub_tenant_id) VALUES ($1,$2)`, ws, tn)
	}
	for ws, us := range map[string][]string{"alpha": {uApp, uAutre}, "beta": {uAutre}} {
		for _, u := range us {
			exec(t, db, `INSERT INTO user_workspaces (user_id,workspace_id,role,permissions,created_at,updated_at) VALUES ($1,$2,'member','{}',NOW(),NOW())`, u, ws)
		}
	}
	return &Store{DB: db}, db
}

func nFils(t *testing.T, db *sql.DB, where string, args ...any) int {
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_ia_fils WHERE `+where, args...).Scan(&n))
	return n
}

func TestPG_PutGet_TelsQuels_EtAutreTriplet(t *testing.T) {
	s, db := monde(t)
	ctx := context.Background()
	// Les clés ne sont ni triées ni dédoublées : le texte du JSON est rendu à l'identique.
	brut := json.RawMessage(`[{"role":"user","id":"m1","parts":[{"type":"text","text":"é"}],"z":1,"a":2}]`)
	ok, err := s.Put(ctx, session1, "alpha", hubUser, hubTenant, brut, 1)
	require.NoError(t, err)
	require.True(t, ok)
	got, found, err := s.Get(ctx, session1, hubUser, hubTenant)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, string(brut), string(got))

	// Un autre triplet : Get rend « absent », Put ne passe pas et n'écrit rien.
	_, found, err = s.Get(ctx, session1, hubAutre, hubTenant)
	require.NoError(t, err)
	assert.False(t, found)
	_, found, err = s.Get(ctx, session1, hubUser, autreOrg)
	require.NoError(t, err)
	assert.False(t, found)
	for _, tr := range [][2]string{{hubAutre, hubTenant}, {hubUser, autreOrg}} {
		ok, err = s.Put(ctx, session1, "alpha", tr[0], tr[1], json.RawMessage(`[{"pirate":true}]`), 1)
		require.NoError(t, err)
		assert.False(t, ok)
	}
	got, _, _ = s.Get(ctx, session1, hubUser, hubTenant)
	assert.Equal(t, string(brut), string(got))

	// Même triplet : remplacé, compté.
	ok, err = s.Put(ctx, session1, "alpha", hubUser, hubTenant, json.RawMessage(`[{"id":"m2"},{"id":"m3"}]`), 2)
	require.NoError(t, err)
	assert.True(t, ok)
	var n int
	require.NoError(t, db.QueryRow(`SELECT message_count FROM veridian_ia_fils WHERE session_id=$1`, session1).Scan(&n))
	assert.Equal(t, 2, n)
}

func TestPG_DeuxAuteursEnParallele_UnSeulGagne(t *testing.T) {
	s, db := monde(t)
	var gagnants int32
	var wg sync.WaitGroup
	for _, u := range []string{hubUser, hubAutre} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			if ok, err := s.Put(context.Background(), session1, "alpha", u, hubTenant, json.RawMessage(`[{"id":"x"}]`), 1); err == nil && ok {
				atomic.AddInt32(&gagnants, 1)
			}
		}(u)
	}
	wg.Wait()
	assert.EqualValues(t, 1, gagnants)
	assert.Equal(t, 1, nFils(t, db, `session_id = $1`, session1))
}

func TestPG_Resolve_LEspaceDeLOrganisation_EtLaPlaceDeLaPersonne(t *testing.T) {
	s, db := monde(t)
	ctx := context.Background()
	ws, err := s.Resolve(ctx, hubTenant, hubUser)
	require.NoError(t, err)
	assert.Equal(t, "alpha", ws, "l'espace relié à l'organisation, jamais un autre")
	// hubUser n'est pas membre de « beta ».
	_, err = s.Resolve(ctx, autreOrg, hubUser)
	assert.ErrorIs(t, err, ErrNotMember)
	_, err = s.Resolve(ctx, "99999999-9999-4999-8999-999999999999", hubUser)
	assert.ErrorIs(t, err, ErrUnlinked)
	// Retiré de l'espace (par la console, sans le Hub) : refusé tout de suite.
	exec(t, db, `DELETE FROM user_workspaces WHERE user_id=$1 AND workspace_id='alpha'`, uApp)
	_, err = s.Resolve(ctx, hubTenant, hubUser)
	assert.ErrorIs(t, err, ErrNotMember)
	// Compte supprimé au Hub : refusé.
	_, err = s.Resolve(ctx, hubTenant, hubAutre)
	require.NoError(t, err)
	exec(t, db, `UPDATE users SET hub_deleted_at=NOW() WHERE id=$1`, uAutre)
	_, err = s.Resolve(ctx, hubTenant, hubAutre)
	assert.ErrorIs(t, err, ErrNotMember)
	// Espace supprimé : l'organisation n'a plus d'espace.
	exec(t, db, `UPDATE veridian_plan SET deleted_at=NOW() WHERE workspace_id='alpha'`)
	_, err = s.Resolve(ctx, hubTenant, hubUser)
	assert.ErrorIs(t, err, ErrUnlinked)
}

// CONTRAT-HUB §5.24 : « retrait d'un membre : ses fils dans cette organisation supprimés ». Seuls les siens, seulement dans cet espace.
func TestPG_RetraitDuMembre_SesFilsPartent_PasCeuxDesAutresNiDAilleurs(t *testing.T) {
	s, db := monde(t)
	ctx := context.Background()
	const s2, s3, s4 = "88888888-8888-4888-8888-888888888881", "88888888-8888-4888-8888-888888888882", "88888888-8888-4888-8888-888888888883"
	msg := json.RawMessage(`[{"id":"m"}]`)
	for _, c := range []struct{ s, ws, u, tn string }{
		{session1, "alpha", hubAutre, hubTenant}, {s2, "alpha", hubAutre, hubTenant}, // à supprimer
		{s3, "alpha", hubUser, hubTenant}, // une autre personne
		{s4, "beta", hubAutre, autreOrg},  // la même personne, une autre organisation
	} {
		ok, err := s.Put(ctx, c.s, c.ws, c.u, c.tn, msg, 1)
		require.NoError(t, err)
		require.True(t, ok)
	}
	n, err := s.DeleteMember(ctx, "alpha", hubAutre)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 0, nFils(t, db, `hub_user_id=$1 AND workspace_id='alpha'`, hubAutre))
	assert.Equal(t, 1, nFils(t, db, `session_id=$1`, s3), "le fil d'une autre personne reste")
	assert.Equal(t, 1, nFils(t, db, `session_id=$1`, s4), "son fil dans une autre organisation reste")
	n, err = s.DeleteMember(ctx, "alpha", hubAutre)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "rejouable")
}

func TestPG_SuppressionDeLOrganisation_TousLesFilsDeLEspacePartent(t *testing.T) {
	s, db := monde(t)
	ctx := context.Background()
	msg := json.RawMessage(`[{"id":"m"}]`)
	for i, c := range []struct{ ws, u, tn string }{{"alpha", hubUser, hubTenant}, {"alpha", hubAutre, hubTenant}, {"beta", hubAutre, autreOrg}} {
		ok, err := s.Put(ctx, "88888888-8888-4888-8888-88888888888"+string(rune('1'+i)), c.ws, c.u, c.tn, msg, 1)
		require.NoError(t, err)
		require.True(t, ok)
	}
	n, err := s.DeleteWorkspace(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 0, nFils(t, db, `workspace_id='alpha'`))
	assert.Equal(t, 1, nFils(t, db, `workspace_id='beta'`), "une autre organisation n'est pas touchée")
	// La purge (suppression de la ligne d'espace) emporte aussi ce qui reste : cascade.
	exec(t, db, `DELETE FROM workspaces WHERE id='beta'`)
	assert.Equal(t, 0, nFils(t, db, `true`))
}

func TestPG_HubUserOf_HubTenantOf(t *testing.T) {
	s, db := monde(t)
	ctx := context.Background()
	u, err := s.HubUserOf(ctx, uApp)
	require.NoError(t, err)
	assert.Equal(t, hubUser, u)
	tn, err := s.HubTenantOf(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, hubTenant, tn)
	tn, err = s.HubTenantOf(ctx, "inconnu")
	require.NoError(t, err)
	assert.Equal(t, "", tn)
	exec(t, db, `UPDATE users SET hub_deleted_at=NOW() WHERE id=$1`, uApp)
	u, err = s.HubUserOf(ctx, uApp)
	require.NoError(t, err)
	assert.Equal(t, "", u, "un compte supprimé au Hub n'a plus de fenêtre")
}
