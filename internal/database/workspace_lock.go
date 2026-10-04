package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Notifuse/notifuse/config"
)

// Veridian fork - durcissement 2026-10-04 (audit securite, "race entre wipe
// et recreation"). Preuve de l'audit : un workspace recree avec le meme id
// qu'un tenant juste wipe pouvait heriter de la base physique de l'ancien
// tenant, parce que EnsureWorkspaceDatabaseExists se contentait de "la base
// existe ? alors ne rien faire" sans jamais se demander SI ELLE DEVRAIT
// encore exister.
//
// Deux couches, comme pour le SSRF (webhook_ssrf_guard.go) :
//  1. acquireWorkspaceLock serialise la creation ET le wipe du MEME
//     workspace id via un advisory lock Postgres nomme. Verifie
//     empiriquement sur un Postgres 17 jetable (2026-10-04) : les advisory
//     locks sont scoppes PAR BASE, jamais partages entre deux bases -
//     c'est pourquoi ce lock DOIT etre pris sur une connexion a la base
//     SYSTEME (notifuse_system), la meme base que celle utilisee par
//     wipeOneTenant / VeridianForceDropDatabase (r.systemDB) cote
//     repository. Un lock pris sur "postgres" (la base de maintenance
//     utilisee pour CREATE/DROP DATABASE) ne bloquerait RIEN cote wipe.
//  2. workspaceSystemRecordExists referme la fenetre residuelle : si la
//     base physique existe deja (le lock a ete pris, donc aucun wipe n'est
//     EN COURS sur cet id) mais qu'aucun record systeme ne la reference,
//     c'est un orphelin (wipe interrompu avant cette mission, ou GC pas
//     encore passe) - EnsureWorkspaceDatabaseExists refuse plutot que de
//     laisser un nouveau tenant heriter de donnees qui ne sont pas les
//     siennes.
//
// La cle de verrou ("veridian_workspace_lock:<id>") est un texte hashe cote
// SQL (hashtext) : aucun code partage requis entre packages, Postgres
// calcule la meme cle des deux cotes a partir du meme texte.
const workspaceLockKeyPrefix = "veridian_workspace_lock:"

// acquireWorkspaceLock prend un advisory lock Postgres SESSION-LEVEL nomme
// sur workspaceID, sur une connexion dediee a la base SYSTEME (pas
// "postgres"). Retourne une fonction de liberation idempotente a appeler en
// defer: elle unlock puis ferme la connexion dediee. Erreur si la base
// systeme est injoignable ou si l'acquisition echoue - l'appelant doit alors
// ABANDONNER l'operation (ne jamais continuer sans le verrou: c'est exactement
// la situation qu'on ne sait plus arbitrer en securite).
func acquireWorkspaceLock(cfg *config.DatabaseConfig, workspaceID string) (release func(), err error) {
	sysDB, err := sql.Open("postgres", GetSystemDSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to system database: %w", err)
	}

	ctx := context.Background()
	conn, err := sysDB.Conn(ctx)
	if err != nil {
		_ = sysDB.Close()
		return nil, fmt.Errorf("failed to acquire system database connection: %w", err)
	}

	lockKey := workspaceLockKeyPrefix + workspaceID
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext($1))", lockKey); err != nil {
		_ = conn.Close()
		_ = sysDB.Close()
		return nil, fmt.Errorf("failed to take advisory lock: %w", err)
	}

	released := false
	release = func() {
		if released {
			return
		}
		released = true
		// Best-effort: meme si l'unlock explicite echoue (connexion deja
		// perdue par exemple), fermer la connexion termine la session cote
		// Postgres et libere le lock session-level de toute facon.
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtext($1))", lockKey)
		_ = conn.Close()
		_ = sysDB.Close()
	}
	return release, nil
}

// workspaceSystemRecordExists indique si un record existe pour cet id dans
// la table `workspaces` de la base systeme. Utilise une connexion separee
// (pas celle qui tient le lock, pour rester independant du cycle de vie du
// lock) ; appele uniquement quand la base physique du workspace existe deja,
// pour detecter un orphelin issu d'un wipe incomplet.
func workspaceSystemRecordExists(cfg *config.DatabaseConfig, workspaceID string) (bool, error) {
	sysDB, err := sql.Open("postgres", GetSystemDSN(cfg))
	if err != nil {
		return false, fmt.Errorf("failed to connect to system database: %w", err)
	}
	defer func() { _ = sysDB.Close() }()

	var exists bool
	err = sysDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM workspaces WHERE id = $1)`, workspaceID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
