package boxaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	_ "github.com/lib/pq"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testpg.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	name := fmt.Sprintf("boxacc_%d", time.Now().UnixNano())
	for _, q := range []string{"CREATE SCHEMA " + name, "SET search_path TO " + name} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.Exec("DROP SCHEMA " + name + " CASCADE"); db.Close() })
	for _, sp := range veridianmig.Default.List() {
		if sp.Number == 75 || sp.Number == 80 {
			for _, q := range sp.System {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return db
}

func TestNewSecret(t *testing.T) {
	a, err := NewSecret()
	b, _ := NewSecret()
	if err != nil || len(a) != 32 || a == b || a != strings.ToLower(a) {
		t.Fatalf("%q %q %v", a, b, err)
	}
	if NormalizeAddress("  JC@Boite.FR ") != "jc@boite.fr" {
		t.Fatal("adresse")
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint("JC@Boite.fr ")
	if len(a) != 12 || a != Fingerprint("jc@boite.fr") || a == Fingerprint("autre@boite.fr") || strings.Contains(a, "boite") {
		t.Fatal(a)
	}
}

func TestAccess_CreateVerifyRevoke(t *testing.T) {
	db := testDB(t)
	s, ctx := NewStore(db), context.Background()
	a, secret, err := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "JC@Boite.fr", Label: "Téléphone", CanSend: true, CreatedBy: "humain:u1"})
	if err != nil || a.Address != "jc@boite.fr" || a.DailyCap != DefaultDailyCap {
		t.Fatalf("%+v %v", a, err)
	}
	var h string
	if err := db.QueryRow(`SELECT secret_hash FROM veridian_box_access`).Scan(&h); err != nil || strings.Contains(h, secret) || len(h) != 64 {
		t.Fatal("le mot de passe ne doit jamais être en base")
	}
	if got, err := s.Verify(ctx, " jc@boite.FR ", secret); err != nil || got.ID != a.ID || got.WorkspaceID != "ws" {
		t.Fatalf("%v %v", got, err)
	}
	for name, p := range map[string][2]string{"faux": {"jc@boite.fr", "faux"}, "vide": {"jc@boite.fr", ""}, "autre adresse": {"x@boite.fr", secret}, "sans adresse": {"", secret}, "trop long": {"jc@boite.fr", strings.Repeat("a", 500)}} {
		if _, err := s.Verify(ctx, p[0], p[1]); !errors.Is(err, ErrAuth) {
			t.Errorf("%s : %v", name, err)
		}
	}
	// Deux accès pour la même boîte, dans deux workspaces : chacun ouvre le sien.
	b, secretB, err := s.Create(ctx, Access{WorkspaceID: "ws2", ProfileID: "p2", Address: "jc@boite.fr", CanSend: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Verify(ctx, "jc@boite.fr", secretB); got == nil || got.WorkspaceID != "ws2" || got.ID != b.ID {
		t.Fatalf("%+v", got)
	}
	if _, err := s.Revoke(ctx, "ws2", a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("un workspace ne révoque pas l'accès d'un autre : %v", err)
	}
	if err := s.Touch(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Alive(ctx, a.ID); err != nil || got.LastUsedAt == nil {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := s.Revoke(ctx, "ws", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, "jc@boite.fr", secret); !errors.Is(err, ErrAuth) {
		t.Fatalf("accès révoqué encore valable : %v", err)
	}
	if _, err := s.Alive(ctx, a.ID); !errors.Is(err, ErrAuth) {
		t.Fatalf("%v", err)
	}
	l, err := s.List(ctx, "ws")
	if err != nil || len(l) != 1 || l[0].RevokedAt == nil {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestAccess_DailyCapIsAtomicAndResets(t *testing.T) {
	db := testDB(t)
	s, ctx := NewStore(db), context.Background()
	now := time.Date(2026, 11, 23, 23, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	a, _, err := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "a@b.fr", CanSend: true, DailyCap: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveSend(ctx, a.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveSend(ctx, a.ID, 2); !errors.Is(err, ErrCap) {
		t.Fatalf("plafond dépassé : %v", err)
	}
	if err := s.ReserveSend(ctx, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if err := s.ReserveSend(ctx, a.ID, 3); err != nil {
		t.Fatalf("jour suivant : %v", err)
	}
	ro, _, _ := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "a@b.fr", CanSend: false})
	if err := s.ReserveSend(ctx, ro.ID, 1); !errors.Is(err, ErrCap) {
		t.Fatalf("un accès en lecture seule n'envoie pas : %v", err)
	}
}

func TestAccess_MaxPerWorkspace(t *testing.T) {
	db := testDB(t)
	s, ctx := NewStore(db), context.Background()
	if _, err := db.Exec(`INSERT INTO veridian_box_access (id, workspace_id, profile_id, address, secret_hash) SELECT 'x' || g, 'ws', 'p', 'a@b.fr', 'h' FROM generate_series(1, $1) g`, MaxPerWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(ctx, Access{WorkspaceID: "ws", ProfileID: "p", Address: "a@b.fr"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("%v", err)
	}
}

func TestStore_Held(t *testing.T) {
	s, ctx := NewStore(testDB(t)), context.Background()
	const u1, u2 = "0a000000-0000-4000-8000-000000000001", "0a000000-0000-4000-8000-000000000002"
	mk := func(ws, holder string) string {
		a, _, err := s.Create(ctx, Access{WorkspaceID: ws, ProfileID: "p", Address: "jc@boite.fr", CanSend: true, CreatedBy: "humain:" + u1, Holder: holder})
		if err != nil || a.Holder != holder {
			t.Fatalf("%+v %v", a, err)
		}
		return a.ID
	}
	mine, gone := mk("ws", u1), mk("ws", u1)
	mk("ws", u2)
	mk("ws", "") // accès d'espace, même auteur : jamais rattaché à la personne
	mk("ws2", u1)
	if _, err := s.Revoke(ctx, "ws", gone); err != nil {
		t.Fatal(err)
	}
	l, err := s.Held(ctx, "ws", u1)
	if err != nil || len(l) != 1 || l[0].ID != mine {
		t.Fatalf("%+v %v", l, err)
	}
	for _, q := range [][2]string{{"", u1}, {"ws", ""}, {"ws", "pas-un-uuid"}} {
		if _, err := s.Held(ctx, q[0], q[1]); err == nil {
			t.Error("espace et personne requis")
		}
	}
}
