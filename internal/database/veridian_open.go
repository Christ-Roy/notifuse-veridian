package database

import (
	"database/sql"

	"github.com/Notifuse/notifuse/pkg/pgdial"
)

// OpenPostgres ouvre un pool avec le transport durci (TCP_USER_TIMEOUT + keepalive, fiche 81) : une
// connexion morte apres une bascule de la grappe ne bloque plus une requete ~15 min. Appele par app.go
// (fichier gele : une ligne) pour la base systeme.
func OpenPostgres(driverName, dsn string) (*sql.DB, error) {
	return pgdial.Open(driverName, dsn)
}
