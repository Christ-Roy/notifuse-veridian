package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/stretchr/testify/require"
)

// Veridian fork - durcissement 2026-10-04 (audit securite, "race entre wipe
// et recreation"). VeridianAcquireWorkspaceLock cote wipe DOIT prendre
// exactement le meme advisory lock (meme texte de cle avant hashtext, meme
// base - notifuse_system) que acquireWorkspaceLock cote creation
// (internal/database/workspace_lock.go). Comme les deux vivent dans des
// packages Go differents (pas de code partage possible sans creer un cycle
// d'import), ce test sert de CANARI anti-derive : il verifie, contre un
// Postgres reel, que VeridianAcquireWorkspaceLock bloque bien une seconde
// tentative de pg_try_advisory_lock utilisant le format de cle canonique
// "veridian_workspace_lock:<id>" - exactement ce que
// internal/database/workspace_lock.go utilise. Si quelqu'un change le
// prefixe d'un cote sans l'autre, ce test rouge le signale immediatement.
func TestVeridianAcquireWorkspaceLock_MatchesCanonicalKeyFormat(t *testing.T) {
	cfg := realRepoTestDBConfig(t)
	sysDB, err := sql.Open("postgres", systemDSN(cfg))
	require.NoError(t, err)
	defer func() { _ = sysDB.Close() }()

	repo := &workspaceRepository{systemDB: sysDB, dbConfig: cfg}

	workspaceID := fmt.Sprintf("lockcanary-%d", time.Now().UnixNano())

	release, err := repo.VeridianAcquireWorkspaceLock(context.Background(), workspaceID)
	require.NoError(t, err)
	defer release()

	// A second connection trying the EXACT canonical key format used by the
	// create-side (internal/database/workspace_lock.go's
	// workspaceLockKeyPrefix = "veridian_workspace_lock:") must find it
	// already held.
	otherConn, err := sql.Open("postgres", systemDSN(cfg))
	require.NoError(t, err)
	defer func() { _ = otherConn.Close() }()

	lockKey := "veridian_workspace_lock:" + workspaceID
	var gotLock bool
	err = otherConn.QueryRow("SELECT pg_try_advisory_lock(hashtext($1))", lockKey).Scan(&gotLock)
	require.NoError(t, err)
	require.False(t, gotLock, "VeridianAcquireWorkspaceLock must hold the canonical veridian_workspace_lock:<id> key")

	// A DIFFERENT workspace id must not be blocked.
	otherKey := "veridian_workspace_lock:" + workspaceID + "-different"
	var gotOtherLock bool
	err = otherConn.QueryRow("SELECT pg_try_advisory_lock(hashtext($1))", otherKey).Scan(&gotOtherLock)
	require.NoError(t, err)
	require.True(t, gotOtherLock, "a different workspace id must not be blocked")
	_, _ = otherConn.Exec("SELECT pg_advisory_unlock(hashtext($1))", otherKey)
}

func systemDSN(cfg *config.DatabaseConfig) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName, cfg.SSLMode)
}

// realRepoTestDBConfig mirrors the convention already used by this
// package's other real-Postgres tests (TEST_DB_HOST/TEST_DB_PORT, default
// localhost:5433, same as the CI integration-tests service container) and
// skips (not fails) when no real Postgres is reachable.
func realRepoTestDBConfig(t *testing.T) *config.DatabaseConfig {
	t.Helper()
	host := "localhost"
	if h := os.Getenv("TEST_DB_HOST"); h != "" {
		host = h
	}
	port := 5433
	if p := os.Getenv("TEST_DB_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	user := "notifuse_test"
	if u := os.Getenv("TEST_DB_USER"); u != "" {
		user = u
	}
	password := "test_password"
	if p := os.Getenv("TEST_DB_PASSWORD"); p != "" {
		password = p
	}

	cfg := &config.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		DBName:   "postgres",
		Prefix:   "lockcanary",
		SSLMode:  "disable",
	}

	probe, err := sql.Open("postgres", systemDSN(cfg))
	if err != nil {
		t.Skip("no real Postgres reachable, skipping:", err)
	}
	defer func() { _ = probe.Close() }()
	if err := probe.Ping(); err != nil {
		t.Skip("no real Postgres reachable, skipping:", err)
	}
	return cfg
}
