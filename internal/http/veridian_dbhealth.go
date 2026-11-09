package http

import (
	"context"
	"database/sql"
	"time"
)

const (
	healthPingFirstTimeout = 1500 * time.Millisecond // sur une connexion du pool, peut-etre morte
	healthPingRetryTimeout = 3 * time.Second         // sur une connexion neuve ; le total reste sous les 5 s du check Nomad
)

// pingWithRecovery verifie la base systeme sans condamner l'application pour des connexions mortes.
//
// Apres une bascule de la grappe (09/11/2026, 18:05 UTC) le pool gardait des connexions inactives mortes :
// chaque verification de sante en consommait une et echouait, quatre echecs de suite faisaient redemarrer
// la tache par Nomad (2 min 30 de coupure contre 10 a 14 s pour les apps qui se reprennent seules).
// Au premier echec on jette donc les connexions inactives du pool et on reessaie une fois sur une connexion
// neuve : le redemarrage reste le dernier filet, pas le premier.
func pingWithRecovery(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), healthPingFirstTimeout)
	err := db.PingContext(ctx)
	cancel()
	if err == nil {
		return nil
	}
	flushIdleConns(db)
	ctx, cancel = context.WithTimeout(context.Background(), healthPingRetryTimeout)
	defer cancel()
	return db.PingContext(ctx)
}

// flushIdleConns ferme les connexions inactives (database/sql ne fait pas autrement) puis rend la capacite.
func flushIdleConns(db *sql.DB) {
	keep := db.Stats().MaxOpenConnections / 2
	if keep < 1 {
		keep = 1
	}
	db.SetMaxIdleConns(0)
	db.SetMaxIdleConns(keep)
}
