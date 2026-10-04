package database

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/config"
	_ "github.com/lib/pq" // PostgreSQL driver
)

// GetConnectionPoolSettings returns connection pool settings based on environment
func GetConnectionPoolSettings() (maxOpen, maxIdle int, maxLifetime time.Duration) {
	environment := os.Getenv("ENVIRONMENT")

	// Use smaller pools for test environment to conserve connections
	if environment == "test" || os.Getenv("INTEGRATION_TESTS") == "true" {
		return 10, 5, 2 * time.Minute
	}

	// Production settings
	return 25, 25, 20 * time.Minute
}

// GetSystemDSN returns the DSN for the system database
func GetSystemDSN(cfg *config.DatabaseConfig) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.DBName,
		cfg.SSLMode,
	)
}

// GetPostgresDSN returns the DSN for connecting to PostgreSQL server without specifying a database
func GetPostgresDSN(cfg *config.DatabaseConfig) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/postgres?sslmode=%s",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.SSLMode,
	)
}

// GetWorkspaceDSN returns the DSN for a workspace database
func GetWorkspaceDSN(cfg *config.DatabaseConfig, workspaceID string) string {
	// Replace hyphens with underscores for PostgreSQL compatibility
	safeID := strings.ReplaceAll(workspaceID, "-", "_")
	dbName := fmt.Sprintf("%s_ws_%s", cfg.Prefix, safeID)
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		dbName,
		cfg.SSLMode,
	)
}

// ConnectToWorkspace creates a new database connection for a workspace
func ConnectToWorkspace(cfg *config.DatabaseConfig, workspaceID string) (*sql.DB, error) {
	// Ensure the workspace database exists
	if err := EnsureWorkspaceDatabaseExists(cfg, workspaceID); err != nil {
		return nil, fmt.Errorf("failed to ensure workspace database exists: %w", err)
	}

	dsn := GetWorkspaceDSN(cfg, workspaceID)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to workspace database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping workspace database: %w", err)
	}

	// Set connection pool settings based on environment
	maxOpen, maxIdle, maxLifetime := GetConnectionPoolSettings()
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxLifetime / 2)

	return db, nil
}

// EnsureWorkspaceDatabaseExists creates the workspace database if it doesn't exist
func EnsureWorkspaceDatabaseExists(cfg *config.DatabaseConfig, workspaceID string) error {
	// Replace hyphens with underscores for PostgreSQL compatibility
	safeID := strings.ReplaceAll(workspaceID, "-", "_")
	dbName := fmt.Sprintf("%s_ws_%s", cfg.Prefix, safeID)

	// Connect to PostgreSQL server without specifying a database
	pgDSN := GetPostgresDSN(cfg)
	db, err := sql.Open("postgres", pgDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL server: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Test the connection
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping PostgreSQL server: %w", err)
	}

	// Veridian fork — durcissement 2026-10-04 (audit securite, race
	// wipe/recreation d'un meme workspace id). Un advisory lock Postgres NOMME
	// sur cet id serialise cette fonction contre wipeOneTenant
	// (veridian_workspace_lock.go, meme cle "veridian_workspace_lock:<id>",
	// meme base systeme) : les deux ne peuvent plus s'entrelacer sur le meme
	// workspace. Il DOIT etre pris sur une connexion a la base SYSTEME, pas a
	// "postgres" : les advisory locks Postgres sont scoppes PAR BASE (verifie
	// empiriquement) - un lock pris ailleurs ne bloquerait rien cote wipe.
	unlockWorkspace, lockErr := acquireWorkspaceLock(cfg, workspaceID)
	if lockErr != nil {
		return fmt.Errorf("failed to acquire workspace creation lock: %w", lockErr)
	}
	defer unlockWorkspace()

	// Check if database exists
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)"
	err = db.QueryRow(query, dbName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if exists {
		// Veridian fork - un workspace recree avec le meme id ne doit JAMAIS
		// heriter silencieusement de la base physique d'un ancien tenant. Si
		// la base physique existe mais qu'aucun RECORD systeme (table
		// `workspaces` de notifuse_system) ne reference cet id, c'est
		// exactement la signature d'un wipe interrompu/orphelin (DROP de
		// rattrapage pas encore passe, ou jamais configure) : on REFUSE
		// plutot que de reutiliser des donnees qui ne sont pas celles du
		// nouveau tenant. L'operateur nettoie via le GC orphelins existant
		// (admin:gc-orphans) puis recree.
		hasRecord, recErr := workspaceSystemRecordExists(cfg, workspaceID)
		if recErr != nil {
			return fmt.Errorf("failed to verify workspace system record before reusing existing database %q: %w", dbName, recErr)
		}
		if !hasRecord {
			return fmt.Errorf("workspace database %q already exists but no system record references workspace %q: refusing to reuse a possibly orphaned database (run GC/wipe cleanup first)", dbName, workspaceID)
		}
	}


	// Create database if it doesn't exist
	if !exists {
		// Use fmt.Sprintf for proper quoting of identifiers in SQL
		createDBQuery := fmt.Sprintf("CREATE DATABASE %s",
			// Proper quoting to prevent SQL injection
			strings.ReplaceAll(dbName, `"`, `""`))

		_, err = db.Exec(createDBQuery)
		if err != nil {
			return fmt.Errorf("failed to create workspace database: %w", err)
		}

		// Connect to the new database to initialize schema
		wsDB, err := sql.Open("postgres", GetWorkspaceDSN(cfg, workspaceID))
		if err != nil {
			return fmt.Errorf("failed to connect to new workspace database: %w", err)
		}
		defer func() {
			_ = wsDB.Close()
		}()

		// Test the connection
		if err := wsDB.Ping(); err != nil {
			return fmt.Errorf("failed to ping new workspace database: %w", err)
		}

		// Initialize the workspace database schema
		if err := InitializeWorkspaceDatabase(wsDB); err != nil {
			return fmt.Errorf("failed to initialize workspace database schema: %w", err)
		}
	}

	return nil
}

// EnsureSystemDatabaseExists creates the system database if it doesn't exist
func EnsureSystemDatabaseExists(dsn string, dbName string) error {
	// Connect to PostgreSQL server without specifying a database
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to connect to PostgreSQL server: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Test the connection
	if err := db.Ping(); err != nil {
		return fmt.Errorf("failed to ping PostgreSQL server: %w", err)
	}

	// Check if database exists
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)"
	err = db.QueryRow(query, dbName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	// Create database if it doesn't exist
	if !exists {
		// Use fmt.Sprintf for proper quoting of identifiers in SQL
		createDBQuery := fmt.Sprintf("CREATE DATABASE %s",
			// Proper quoting to prevent SQL injection
			strings.ReplaceAll(dbName, `"`, `""`))

		_, err = db.Exec(createDBQuery)
		if err != nil {
			return fmt.Errorf("failed to create system database: %w", err)
		}
	}

	return nil
}
