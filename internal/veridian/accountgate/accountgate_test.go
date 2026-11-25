package accountgate

import (
	"context"
	"database/sql"
	"errors"
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

func (b bench) remove(t *testing.T) {
	t.Helper()
	if _, err := b.db.Exec(`UPDATE users SET hub_deleted_at = NOW() WHERE id = $1`, b.uid); err != nil {
		t.Fatal(err)
	}
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
