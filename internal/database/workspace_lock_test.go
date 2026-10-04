package database

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/config"
)

// realTestDBConfig builds a config.DatabaseConfig pointed at a real,
// disposable Postgres (same connection parameters the CI integration-tests
// job already provisions: postgres:17, user/password notifuse_test,
// TEST_DB_HOST/TEST_DB_PORT overridable). This test is skipped (not failed)
// when that Postgres is unreachable, matching the existing convention in
// this package's other DB-dependent tests.
func realTestDBConfig(t *testing.T, sysDBName, prefix string) *config.DatabaseConfig {
	t.Helper()
	host := getEnvOrDefaultLocal("TEST_DB_HOST", "localhost")
	port := 5433
	if p := os.Getenv("TEST_DB_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	cfg := &config.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     getEnvOrDefaultLocal("TEST_DB_USER", "notifuse_test"),
		Password: getEnvOrDefaultLocal("TEST_DB_PASSWORD", "test_password"),
		DBName:   sysDBName,
		Prefix:   prefix,
		SSLMode:  "disable",
	}

	// Probe the maintenance "postgres" database; skip (not fail) if no real
	// Postgres is reachable in this environment.
	probe, err := sql.Open("postgres", GetPostgresDSN(cfg))
	if err != nil {
		t.Skip("no real Postgres reachable, skipping workspace lock integration test:", err)
	}
	defer func() { _ = probe.Close() }()
	if err := probe.Ping(); err != nil {
		t.Skip("no real Postgres reachable, skipping workspace lock integration test:", err)
	}
	return cfg
}

func getEnvOrDefaultLocal(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// createThrowawaySystemDB creates a fresh "system" database with the single
// `workspaces` table EnsureWorkspaceDatabaseExists/workspaceSystemRecordExists
// actually query, and registers its cleanup.
func createThrowawaySystemDB(t *testing.T, cfg *config.DatabaseConfig) {
	t.Helper()
	maint, err := sql.Open("postgres", GetPostgresDSN(cfg))
	if err != nil {
		t.Fatalf("connect to maintenance db: %v", err)
	}
	defer func() { _ = maint.Close() }()

	if _, err := maint.Exec(fmt.Sprintf("CREATE DATABASE %s", cfg.DBName)); err != nil {
		t.Fatalf("create throwaway system db: %v", err)
	}
	t.Cleanup(func() {
		m2, err := sql.Open("postgres", GetPostgresDSN(cfg))
		if err != nil {
			return
		}
		defer func() { _ = m2.Close() }()
		_, _ = m2.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", cfg.DBName))
	})

	sysDB, err := sql.Open("postgres", GetSystemDSN(cfg))
	if err != nil {
		t.Fatalf("connect to throwaway system db: %v", err)
	}
	defer func() { _ = sysDB.Close() }()
	if _, err := sysDB.Exec(`CREATE TABLE workspaces (id text primary key)`); err != nil {
		t.Fatalf("create workspaces table: %v", err)
	}
}

// dropWorkspaceDBIfAny is test cleanup for a physical workspace database
// created directly by SQL (bypassing EnsureWorkspaceDatabaseExists) to
// simulate an orphan.
func dropWorkspaceDBIfAny(t *testing.T, cfg *config.DatabaseConfig, workspaceID string) {
	t.Helper()
	safeID := workspaceID
	dbName := fmt.Sprintf("%s_ws_%s", cfg.Prefix, safeID)
	t.Cleanup(func() {
		maint, err := sql.Open("postgres", GetPostgresDSN(cfg))
		if err != nil {
			return
		}
		defer func() { _ = maint.Close() }()
		_, _ = maint.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName))
	})
}

// TestEnsureWorkspaceDatabaseExists_RefusesOrphanedPhysicalDatabase
// reproduces the EXACT 2026-10-04 audit finding: a workspace id whose
// physical database already exists but has NO matching system record
// (the signature of an interrupted/incomplete wipe) must be REFUSED, never
// silently reused — a brand-new tenant must never inherit a previous
// tenant's data just because it happened to get the same workspace id.
func TestEnsureWorkspaceDatabaseExists_RefusesOrphanedPhysicalDatabase(t *testing.T) {
	sysDBName := fmt.Sprintf("racelock_sys_%d", time.Now().UnixNano())
	cfg := realTestDBConfig(t, sysDBName, "racelock")
	createThrowawaySystemDB(t, cfg)

	workspaceID := "orphantest1"
	dropWorkspaceDBIfAny(t, cfg, workspaceID)

	// Simulate an orphan: the physical database exists (as if a wipe had
	// deleted the system record but the DROP never ran / hadn't run yet),
	// but no system record references this workspace id.
	maint, err := sql.Open("postgres", GetPostgresDSN(cfg))
	if err != nil {
		t.Fatalf("connect to maintenance db: %v", err)
	}
	defer func() { _ = maint.Close() }()
	dbName := fmt.Sprintf("%s_ws_%s", cfg.Prefix, workspaceID)
	if _, err := maint.Exec(fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		t.Fatalf("pre-create orphan workspace db: %v", err)
	}

	// RED (pre-fix behavior): this used to return nil and silently let the
	// caller connect to (and reuse) the stale database. GREEN (post-fix):
	// refused with an explicit, actionable error.
	err = EnsureWorkspaceDatabaseExists(cfg, workspaceID)
	if err == nil {
		t.Fatal("expected EnsureWorkspaceDatabaseExists to refuse an orphaned physical database, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to reuse a possibly orphaned database") {
		t.Fatalf("expected an orphan-refusal error, got: %v", err)
	}

	// Now add the missing system record (what a legitimate CreateWorkspace
	// would have done) — EnsureWorkspaceDatabaseExists must then accept the
	// existing database normally (this is NOT a regression: a real,
	// recorded workspace reusing its own already-provisioned database is
	// the expected, common case).
	sysDB, err := sql.Open("postgres", GetSystemDSN(cfg))
	if err != nil {
		t.Fatalf("connect to system db: %v", err)
	}
	defer func() { _ = sysDB.Close() }()
	if _, err := sysDB.Exec(`INSERT INTO workspaces (id) VALUES ($1)`, workspaceID); err != nil {
		t.Fatalf("insert system record: %v", err)
	}

	if err := EnsureWorkspaceDatabaseExists(cfg, workspaceID); err != nil {
		t.Fatalf("expected no error once the system record exists, got: %v", err)
	}
}

// TestAcquireWorkspaceLock_SerializesSameWorkspaceID proves the advisory
// lock actually serializes two concurrent operations on the SAME workspace
// id (the core of the wipe/recreate race fix): while the lock is held, a
// second attempt to acquire it for the same id BLOCKS, and only proceeds
// after the first is released.
func TestAcquireWorkspaceLock_SerializesSameWorkspaceID(t *testing.T) {
	sysDBName := fmt.Sprintf("racelock_sys_%d", time.Now().UnixNano())
	cfg := realTestDBConfig(t, sysDBName, "racelock")
	createThrowawaySystemDB(t, cfg)

	workspaceID := "lockrace1"

	release1, err := acquireWorkspaceLock(cfg, workspaceID)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := acquireWorkspaceLock(cfg, workspaceID)
		if err != nil {
			t.Errorf("second acquire failed: %v", err)
			return
		}
		defer release2()
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire must NOT succeed while the first lock is still held")
	case <-time.After(400 * time.Millisecond):
		// Expected: still blocked.
	}

	release1()

	select {
	case <-acquired:
		// Expected: unblocked promptly after release.
	case <-time.After(5 * time.Second):
		t.Fatal("second acquire did not proceed after the first lock was released")
	}
}

// TestAcquireWorkspaceLock_DifferentWorkspaceIDsDoNotBlock is the
// companion "doesn't over-serialize" proof: two DIFFERENT workspace ids
// must not block each other.
func TestAcquireWorkspaceLock_DifferentWorkspaceIDsDoNotBlock(t *testing.T) {
	sysDBName := fmt.Sprintf("racelock_sys_%d", time.Now().UnixNano())
	cfg := realTestDBConfig(t, sysDBName, "racelock")
	createThrowawaySystemDB(t, cfg)

	release1, err := acquireWorkspaceLock(cfg, "ws-a")
	if err != nil {
		t.Fatalf("acquire ws-a failed: %v", err)
	}
	defer release1()

	done := make(chan error, 1)
	go func() {
		release2, err := acquireWorkspaceLock(cfg, "ws-b")
		if err == nil {
			release2()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("acquire ws-b should not fail: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("acquiring a DIFFERENT workspace id must not block on an unrelated lock")
	}
}
