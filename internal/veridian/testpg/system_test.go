package testpg

import (
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

func TestSystem_FreshDatabaseEachTime(t *testing.T) {
	prep := func(db *sql.DB) error { _, err := db.Exec(`CREATE TABLE t (a INT)`); return err }
	a, b := System(t, prep), System(t, prep) // deux bases : la même table se crée deux fois
	if _, err := a.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}
