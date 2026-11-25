package accountgate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

type bench struct {
	db    *sql.DB
	auth  *Auth
	users *Users
	uid   string
}

// Vrais dépôts, vraie base : ce que l'application câble.
func newBench(t *testing.T) bench {
	db := testpg.System(t, func(db *sql.DB) error {
		if err := database.InitializeDatabase(db, ""); err != nil {
			return err
		}
		_, err := veridianmig.Default.ApplySystem(context.Background(), db)
		return err
	})
	b := bench{db: db, auth: &Auth{AuthRepository: repository.NewSQLAuthRepository(db), DB: db},
		users: &Users{UserRepository: repository.NewUserRepository(db), DB: db}, uid: uuid.NewString()}
	if _, err := db.Exec(`INSERT INTO users (id,type,email,created_at,updated_at) VALUES ($1,'user','jc@t.fr',NOW(),NOW())`, b.uid); err != nil {
		t.Fatal(err)
	}
	return b
}

func (b bench) session() *domain.Session {
	return &domain.Session{ID: uuid.NewString(), UserID: b.uid, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
}

func (b bench) remove(t *testing.T) { b.removeAt(t, time.Now()) }

func (b bench) removeAt(t *testing.T, at time.Time) {
	t.Helper()
	if _, err := b.db.Exec(`UPDATE users SET hub_deleted_at = $2 WHERE id = $1`, b.uid, at); err != nil {
		t.Fatal(err)
	}
}

func setSince(t *testing.T, at time.Time) {
	t.Helper()
	SetSince(at)
	t.Cleanup(func() { SetSince(time.Time{}) })
}

func TestReadSince_NeverSilentlyOff(t *testing.T) {
	at := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		value string
		prod  bool
		want  time.Time
		fails bool
	}{
		"posée":                 {"2026-11-25T00:00:00Z", true, at, false},
		"posée, autre fuseau":   {" 2026-11-25T01:00:00+01:00 ", true, at, false},
		"absente en production": {"", true, time.Time{}, true},
		"blanche en production": {"  ", true, time.Time{}, true},
		"absente ailleurs":      {"", false, time.Time{}, false},
		"illisible":             {"25/11/2026", true, time.Time{}, true},
		"illisible ailleurs":    {"off", false, time.Time{}, true},
		"sans heure":            {"2026-11-25", true, time.Time{}, true},
	} {
		got, err := ReadSince(c.value, c.prod)
		if (err != nil) != c.fails || !got.Equal(c.want) {
			t.Errorf("%s : %v %v", name, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), SinceVar) {
			t.Errorf("%s : le refus doit nommer la variable : %v", name, err)
		}
	}
}

func TestSince_ZeroGatesEveryone(t *testing.T) {
	if !Since().Equal(time.Unix(0, 0)) {
		t.Fatal("avant SetSince, tout compte supprimé est coupé")
	}
	at := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	setSince(t, at)
	if !Since().Equal(at) {
		t.Fatal(Since())
	}
	SetSince(time.Time{})
	if !Since().Equal(time.Unix(0, 0)) {
		t.Fatal("une date nulle coupe tout compte supprimé")
	}
}

// Les deux sens : supprimé APRÈS la date, coupé ; supprimé AVANT, comme aujourd'hui, et compté.
func TestGate_OnlyAccountsDeletedSinceTheDate(t *testing.T) {
	b, ctx := newBench(t), context.Background()
	date := time.Now().Add(-time.Hour)
	setSince(t, date)
	s := b.session()
	if err := b.users.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	opens := func() (created, users, auth bool) {
		n := b.session()
		created = b.users.CreateSession(ctx, n) == nil
		_, uerr := b.users.GetSessionByID(ctx, s.ID)
		_, aerr := b.auth.GetSessionByID(ctx, s.ID, b.uid)
		return created, uerr == nil, aerr == nil
	}
	count := func(want int) {
		t.Helper()
		if n, err := CountOpen(ctx, b.db); err != nil || n != want {
			t.Fatalf("comptés : %d %v, attendu %d", n, err, want)
		}
	}
	count(0) // un compte vivant n'est pas compté
	b.removeAt(t, date.Add(-time.Minute))
	if c, u, a := opens(); !c || !u || !a {
		t.Fatalf("supprimé avant la date : rien ne change (%v %v %v)", c, u, a)
	}
	count(1)
	b.removeAt(t, date) // la date elle-même est dedans
	if c, u, a := opens(); c || u || a {
		t.Fatalf("supprimé à la date : coupé (%v %v %v)", c, u, a)
	}
	count(0)
	b.removeAt(t, date.Add(-time.Minute))
	SetSince(date.Add(-time.Hour)) // la date recule : le compte tombe, rien d'autre à coder
	if c, u, a := opens(); c || u || a {
		t.Fatalf("date reculée : coupé (%v %v %v)", c, u, a)
	}
	count(0)
	// Sans place ni session, il n'y a plus rien d'ouvert à compter.
	SetSince(date)
	count(1)
	if _, err := b.db.Exec(`DELETE FROM user_sessions`); err != nil {
		t.Fatal(err)
	}
	count(0)
	if _, err := b.db.Exec(`INSERT INTO user_workspaces (user_id,workspace_id,role,created_at,updated_at) VALUES ($1,'ws','member',NOW(),NOW())`, b.uid); err != nil {
		t.Fatal(err)
	}
	count(1)
}

func TestUsers_CreateSession_DeletedAccountOpensNothing(t *testing.T) {
	b, ctx := newBench(t), context.Background()
	if err := b.users.CreateSession(ctx, b.session()); err != nil {
		t.Fatal(err)
	}
	b.remove(t)
	var nf *domain.ErrUserNotFound
	if err := b.users.CreateSession(ctx, b.session()); !errors.As(err, &nf) {
		t.Fatalf("compte supprimé au Hub : session ouverte (%v)", err)
	}
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM user_sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	// Geste du Hub au nom d'un propriétaire supprimé (transfert de l'espace) : reste possible.
	if err := b.users.CreateSession(context.WithValue(ctx, domain.SystemCallKey, true), b.session()); err != nil {
		t.Fatal(err)
	}
}

func TestUsers_GetSessionByID_And_GetSessionsByUserID(t *testing.T) {
	b, ctx := newBench(t), context.Background()
	s := b.session()
	if err := b.users.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got, err := b.users.GetSessionByID(ctx, s.ID); err != nil || got.UserID != b.uid {
		t.Fatalf("%v %v", got, err)
	}
	if l, err := b.users.GetSessionsByUserID(ctx, b.uid); err != nil || len(l) != 1 {
		t.Fatalf("%v %v", l, err)
	}
	b.remove(t)
	var nf *domain.ErrSessionNotFound
	if _, err := b.users.GetSessionByID(ctx, s.ID); !errors.As(err, &nf) {
		t.Fatalf("session d'un compte supprimé : %v", err)
	}
	if l, err := b.users.GetSessionsByUserID(ctx, b.uid); err != nil || len(l) != 0 {
		t.Fatalf("aucun code de connexion à comparer : %v %v", l, err)
	}
}

func TestAuth_GetSessionByID_DeletedAccountIs401(t *testing.T) {
	b, ctx := newBench(t), context.Background()
	s := b.session()
	if err := b.users.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := b.auth.GetSessionByID(ctx, s.ID, b.uid); err != nil {
		t.Fatal(err)
	}
	b.remove(t)
	if _, err := b.auth.GetSessionByID(ctx, s.ID, b.uid); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session encore valable : %v", err)
	}
	if _, err := b.auth.GetSessionByID(context.WithValue(ctx, domain.SystemCallKey, true), s.ID, b.uid); err != nil {
		t.Fatalf("session interne : %v", err)
	}
	// Une panne de lecture n'ouvre rien et ne se lit pas comme une absence.
	_ = b.db.Close()
	if _, err := b.auth.GetSessionByID(ctx, s.ID, b.uid); err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%v", err)
	}
}
