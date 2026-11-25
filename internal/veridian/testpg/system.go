package testpg

import (
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"
)

// System rend une base neuve, jetée à la fin du test. prepare pose le schéma (schéma système, migrations Veridian) : le paquet n'en importe
// aucun, pour rester utilisable partout.
func System(t testing.TB, prepare func(db *sql.DB) error) *sql.DB {
	t.Helper()
	dsn := DSN(t)
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("veridian_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE DATABASE "` + name + `"`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec(`DROP DATABASE "` + name + `" WITH (FORCE)`)
		_ = admin.Close()
	})
	if err := prepare(db); err != nil {
		t.Fatal(err)
	}
	return db
}
