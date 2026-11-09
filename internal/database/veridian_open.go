package database

import (
	"database/sql"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/pgdial"
)

// OpenPostgres ouvre un pool avec le transport durci (TCP_USER_TIMEOUT + keepalive, fiche 81) : une
// connexion morte apres une bascule de la grappe ne bloque plus une requete ~15 min. Appele par app.go
// (fichier gele : une ligne) pour la base systeme.
func OpenPostgres(driverName, dsn string) (*sql.DB, error) {
	return pgdial.Open(driverName, dsn)
}

// Delai total et pas des nouvelles tentatives de l'initialisation de la base systeme au demarrage.
var (
	initRetryWindow = 90 * time.Second
	initRetryDelay  = 2 * time.Second
	initDatabase    = InitializeDatabase
)

// transientDBStartupError : la base est en bascule, pas cassee (09/11/2026 : apres un retour de bascule, le
// demarrage tombait sur l'ancien primaire devenu replica : « cannot execute CREATE TABLE in a read-only
// transaction », sortie 1, tache redemarree).
func transientDBStartupError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, m := range []string{"read-only transaction", "shutting down", "starting up", "bad connection", "eof",
		"connection refused", "connection reset", "context deadline exceeded", "timeout", "recovery mode"} {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// InitializeDatabaseResilient : InitializeDatabase qui reessaie tant que la base est en bascule (90 s max)
// au lieu de faire sortir l'application en erreur. Une erreur durable (schema, droits) remonte aussitot.
func InitializeDatabaseResilient(db *sql.DB, rootEmail string) error {
	deadline := time.Now().Add(initRetryWindow)
	for {
		err := initDatabase(db, rootEmail)
		if err == nil || !transientDBStartupError(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(initRetryDelay)
	}
}
