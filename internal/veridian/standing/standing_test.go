package standing

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/database"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/accountgate"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	_ "github.com/lib/pq"
)

const (
	uid   = "0a000000-0000-4000-8000-000000000001"
	other = "0a000000-0000-4000-8000-000000000002"
)

func systemDB(t *testing.T) *sql.DB {
	return testpg.System(t, func(db *sql.DB) error {
		if err := database.InitializeDatabase(db, ""); err != nil {
			return err
		}
		_, err := veridianmig.Default.ApplySystem(context.Background(), db)
		return err
	})
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestState_SpaceAndPerson(t *testing.T) {
	ok := State{Workspace: true, Member: true}
	if !ok.Space() || !ok.Person() {
		t.Fatal("un membre d'un espace ouvert passe")
	}
	for name, s := range map[string]State{"espace absent": {Member: true}, "espace fermé": {Workspace: true, Member: true, Closed: true},
		"pas membre": {Workspace: true}, "gelé": {Workspace: true, Member: true, Frozen: true}, "compte supprimé": {Workspace: true, Member: true, Deleted: true}} {
		if s.Person() {
			t.Errorf("%s : refus attendu", name)
		}
	}
	if !(State{Workspace: true, Member: true, Suspended: true}).Person() {
		t.Fatal("suspendu : la lecture reste (CONTRAT-HUB §5.4)")
	}
}

func TestStore_Of(t *testing.T) {
	db, ctx := systemDB(t), context.Background()
	s := &Store{DB: db}
	exec(t, db, `INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ('ws','ws',NOW(),NOW()),('seul','seul',NOW(),NOW())`)
	for _, id := range []string{uid, other} {
		exec(t, db, `INSERT INTO users (id,type,email,created_at,updated_at) VALUES ($1,'user',$2,NOW(),NOW())`, id, id+"@t.fr")
		exec(t, db, `INSERT INTO user_workspaces (user_id,workspace_id,role,created_at,updated_at) VALUES ($1,'ws','member',NOW(),NOW())`, id)
	}
	exec(t, db, `INSERT INTO veridian_plan (workspace_id,plan,status) VALUES ('ws','free','active')`)
	want := func(step, ws, user string, w State) {
		t.Helper()
		got, err := s.Of(ctx, ws, user)
		if err != nil || got != w {
			t.Fatalf("%s : %+v %v, attendu %+v", step, got, err, w)
		}
	}
	in := State{Workspace: true, Member: true}
	want("membre", "ws", uid, in)
	want("sans plan", "seul", uid, State{Workspace: true})
	want("espace absent", "zz", uid, State{})
	want("pas un compte", "ws", "", State{Workspace: true})
	want("pas un UUID", "ws", "x' OR 1=1", State{Workspace: true})

	exec(t, db, `INSERT INTO veridian_frozen_members (workspace_id,user_id,reason) VALUES ('ws',$1,'manual')`, uid)
	want("gelé", "ws", uid, State{Workspace: true, Member: true, Frozen: true})
	want("l'autre ne l'est pas", "ws", other, in)
	exec(t, db, `DELETE FROM veridian_frozen_members`)

	exec(t, db, `UPDATE users SET hub_deleted_at = NOW() WHERE id = $1`, uid)
	want("compte supprimé", "ws", uid, State{Workspace: true, Member: true, Deleted: true})
	want("l'autre vit", "ws", other, in)
	// Supprimé AVANT la date de la garde : rien ne change pour lui (accountgate).
	accountgate.SetSince(time.Now().Add(time.Hour))
	t.Cleanup(func() { accountgate.SetSince(time.Time{}) })
	want("compte supprimé avant la date", "ws", uid, in)
	accountgate.SetSince(time.Now().Add(-time.Hour))
	want("compte supprimé depuis la date", "ws", uid, State{Workspace: true, Member: true, Deleted: true})
	exec(t, db, `UPDATE users SET hub_deleted_at = NULL`)

	exec(t, db, `UPDATE veridian_plan SET status = 'suspended', suspended_at = NOW()`)
	want("suspendu", "ws", uid, State{Workspace: true, Member: true, Suspended: true})
	exec(t, db, `UPDATE veridian_plan SET status = 'active', deleted_at = NOW()`)
	want("fermé", "ws", uid, State{Workspace: true, Member: true, Closed: true})
	exec(t, db, `UPDATE veridian_plan SET deleted_at = NULL`)

	exec(t, db, `DELETE FROM user_workspaces WHERE user_id = $1`, uid)
	want("retiré", "ws", uid, State{Workspace: true})
	want("l'autre reste", "ws", other, in)

	_ = db.Close()
	if _, err := s.Of(ctx, "ws", uid); err == nil {
		t.Fatal("une panne de lecture n'est pas une situation")
	}
}
