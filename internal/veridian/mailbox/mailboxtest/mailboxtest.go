// Package mailboxtest prépare une base Postgres jetable avec le schéma de la boîte unifiée, pour les
// tests des paquets qui dépendent du store (L1_TEST_PG_DSN, sinon le test est ignoré). Jamais importé par le code de production.
package mailboxtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	_ "github.com/lib/pq"
)

var seq int64

// SecretKey est la clé de chiffrement des tests.
const SecretKey = "cle-de-test"

// NewStore rend un store sur un schéma neuf, avec les tables de Notifuse dont il dépend.
func NewStore(t *testing.T) (*mailbox.Store, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("L1_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("L1_TEST_PG_DSN absent : test Postgres non exécuté")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&seq, 1))
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("SET search_path TO " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DROP SCHEMA " + name + " CASCADE")
		db.Close()
	})
	for _, q := range []string{
		`CREATE TABLE contacts (email VARCHAR(255) PRIMARY KEY)`,
		`CREATE TABLE contact_lists (email VARCHAR(255) NOT NULL, list_id VARCHAR(32) NOT NULL, status VARCHAR(20) NOT NULL, PRIMARY KEY (email, list_id))`,
		`CREATE TABLE webhook_subscriptions (id VARCHAR(36) PRIMARY KEY, enabled BOOLEAN NOT NULL DEFAULT true, settings JSONB NOT NULL DEFAULT '{}')`,
		`CREATE TABLE webhook_deliveries (id VARCHAR(36) PRIMARY KEY, subscription_id VARCHAR(36) NOT NULL, event_type VARCHAR(50) NOT NULL, payload JSONB NOT NULL,
			status VARCHAR(20) NOT NULL, attempts INT NOT NULL, max_attempts INT NOT NULL, next_attempt_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE message_history (
			id VARCHAR(255) PRIMARY KEY, contact_email VARCHAR(255) NOT NULL, automation_id VARCHAR(36), broadcast_id VARCHAR(255),
			template_id VARCHAR(32) NOT NULL DEFAULT 'tpl', template_version INTEGER NOT NULL DEFAULT 1, channel VARCHAR(20) NOT NULL DEFAULT 'email',
			message_data JSONB NOT NULL DEFAULT '{}', channel_options JSONB, sent_at TIMESTAMPTZ, failed_at TIMESTAMPTZ,
			veridian_profile_id VARCHAR(255), veridian_sender_email VARCHAR(255))`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range mailbox.SchemaStatements {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schéma: %v\n%s", err, q)
		}
	}
	return mailbox.NewStore(
		func(context.Context, string) (*sql.DB, error) { return db, nil },
		func(context.Context, string) (string, error) { return SecretKey, nil },
	), db
}
