package database

import (
	"database/sql"
	"fmt"

	"github.com/lib/pq"
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

// monitoringRole : role de surveillance de la grappe pg-saas (pg_monitor), qui doit pouvoir se connecter.
const monitoringRole = "pgsaas_mon"

// restrictWorkspaceDatabase ferme une base workspace neuve a PUBLIC (09/11/2026, demande de la surveillance
// pg-saas). Une base creee par CREATE DATABASE nait avec datacl NULL, donc ouverte a tout role du cluster : sur
// la grappe partagee, hub, twenty, cms ou prospection pourraient lire les donnees d'un client Notifuse.
// PostgreSQL n'a pas de defaut pour les futures bases : seul le provisionnement peut le faire. Le proprietaire
// (role de l'appli) garde l'acces ; le role de surveillance n'est servi que s'il existe (dev/staging l'ignorent).
func restrictWorkspaceDatabase(db *sql.DB, dbName, owner string) error {
	qdb := pq.QuoteIdentifier(dbName)
	if _, err := db.Exec(fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC", qdb)); err != nil {
		return fmt.Errorf("failed to revoke public access on workspace database: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", qdb, pq.QuoteIdentifier(owner))); err != nil {
		return fmt.Errorf("failed to grant owner access on workspace database: %w", err)
	}
	var monitored bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)`, monitoringRole).Scan(&monitored); err != nil {
		return fmt.Errorf("failed to look up monitoring role: %w", err)
	}
	if monitored {
		if _, err := db.Exec(fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", qdb, pq.QuoteIdentifier(monitoringRole))); err != nil {
			return fmt.Errorf("failed to grant monitoring access on workspace database: %w", err)
		}
	}
	return nil
}
