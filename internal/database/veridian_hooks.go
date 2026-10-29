package database

import (
	"database/sql"
	"fmt"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
)

// initVeridianWorkspace complète une base workspace neuve avec les migrations Veridian (registre
// internal/migrations/veridian, suivi par numéro). Les lots n'ajoutent donc rien à init.go.
func initVeridianWorkspace(db *sql.DB, workspaceID string) error {
	if err := veridianmig.ApplyToNewWorkspace(db, workspaceID); err != nil {
		return fmt.Errorf("failed to apply Veridian migrations to workspace database: %w", err)
	}
	return nil
}
