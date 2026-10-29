// Package foldertest : base PostgreSQL jetable pour les tests des dossiers (VERIDIAN_TEST_POSTGRES_DSN).
// Chaque appel cree un schema propre avec les tables upstream minimales (segments, lists, templates).
package foldertest

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq"
)

var counter atomic.Int64

// NewDB saute le test si aucune base jetable n'est fournie.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("VERIDIAN_TEST_POSTGRES_DSN absent : test SQL des dossiers non joue")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("l2_%d_%d", os.Getpid(), counter.Add(1))
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	for _, ddl := range []string{
		`CREATE TABLE segments (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL, color VARCHAR(50) NOT NULL DEFAULT '#000',
			tree JSONB NOT NULL DEFAULT '{}', timezone VARCHAR(100) NOT NULL DEFAULT 'UTC', version INTEGER NOT NULL DEFAULT 1,
			status VARCHAR(20) NOT NULL DEFAULT 'active', db_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE lists (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE templates (id VARCHAR(32) NOT NULL, name VARCHAR(255) NOT NULL, version INTEGER NOT NULL, channel VARCHAR(20) NOT NULL DEFAULT 'email',
			category VARCHAR(20) NOT NULL DEFAULT 'marketing', updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), deleted_at TIMESTAMPTZ, PRIMARY KEY (id, version))`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
