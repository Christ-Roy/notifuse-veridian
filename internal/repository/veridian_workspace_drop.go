package repository

// === Veridian patch — 2026-06-18 — DROP DATABASE robuste (FORCE) + GC orphelins ===
//
// POURQUOI ce fichier existe :
//
// Le DROP de base workspace upstream (workspaceRepository.DeleteDatabase) fait :
//   1. pg_terminate_backend sur les connexions de la base
//   2. time.Sleep(100ms)
//   3. DROP DATABASE IF EXISTS <db>      ← SANS WITH (FORCE)
//
// Sur staging, le worker EmailQueueWorker poll TOUS les workspaces en round-robin
// et REOUVRE en permanence des connexions sur chaque base. Entre le terminate (1)
// et le drop (3), le worker rouvre une connexion → PostgreSQL répond
// "database is being accessed by other users" → le DROP ÉCHOUE → la base reste.
// Résultat observé 2026-06-18 : 799 bases physiques notifuse_ws_* pour seulement
// 110 records workspaces → 689 bases orphelines (record wipé, DROP raté).
// Cf. todo/2026-06-17-orphan-workspaces-staging-db-starvation.md.
//
// Le fix : DROP DATABASE ... WITH (FORCE) (PG13+) qui termine les backends
// ATOMIQUEMENT dans la même commande, éliminant la fenêtre de course. La prod
// (Postgres 17) et le staging (Postgres 17-alpine) supportent FORCE.
//
// On NE patche PAS DeleteDatabase upstream (règle Veridian zéro-patch-upstream).
// On expose ici des MÉTHODES veridian sur le workspaceRepository (accès direct à
// r.systemDB / r.dbConfig.Prefix) + des fonctions pures réutilisables. Le service
// les consomme par TYPE-ASSERTION sur une interface étroite (pas d'import du
// package repository depuis service → évite le cycle automation_postgres→service).

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// veridianSafeDBIdent valide qu'un nom de base PostgreSQL ne contient QUE des
// caractères identifiant sûrs ([a-z0-9_]). Le nom est construit programmatiquement
// (prefix_ws_<id>), mais on interpole dans DROP DATABASE (pas de bind param possible
// sur un identifiant) → défense en profondeur stricte contre toute injection.
var veridianSafeDBIdent = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// VeridianWorkspaceDBName construit le nom de base physique d'un workspace, à
// l'identique de workspaceRepository.DeleteDatabase upstream :
// "<prefix>_ws_<workspaceID avec - remplacés par _>".
func VeridianWorkspaceDBName(prefix, workspaceID string) string {
	safeID := strings.ReplaceAll(workspaceID, "-", "_")
	return fmt.Sprintf("%s_ws_%s", prefix, safeID)
}

// VeridianWorkspaceIDFromDBName fait l'inverse de VeridianWorkspaceDBName :
// extrait le segment après "<prefix>_ws_". Comme la conversion "-"→"_" n'est PAS
// bijective, les underscores sont conservés — suffisant pour matcher un préfixe
// de safety (canary, etc.) côté GC.
func VeridianWorkspaceIDFromDBName(prefix, dbName string) string {
	return strings.TrimPrefix(dbName, fmt.Sprintf("%s_ws_", prefix))
}

// VeridianForceDropDatabase est la MÉTHODE consommée par le service (via
// l'interface étroite veridianForceDropper). DROP la base du workspace avec FORCE.
// Idempotent (IF EXISTS), best-effort sur revoke/terminate (cf. doc du helper).
func (r *workspaceRepository) VeridianForceDropDatabase(ctx context.Context, workspaceID string, log logger.Logger) error {
	dbName := VeridianWorkspaceDBName(r.dbConfig.Prefix, workspaceID)
	// Ferme le pool de connexions de ce workspace côté app AVANT le DROP (réduit
	// les backends que le FORCE devra tuer ; non bloquant si ça échoue).
	if r.connectionManager != nil {
		_ = r.connectionManager.CloseWorkspaceConnection(workspaceID)
	}
	return veridianForceDropByDBName(ctx, r.systemDB, dbName, log)
}

// VeridianListOrphanWorkspaceDBs (MÉTHODE) liste les bases notifuse_ws_* sans
// record dans la table system `workspaces`. Délègue au helper pur en lui passant
// r.systemDB + r.dbConfig.Prefix.
func (r *workspaceRepository) VeridianListOrphanWorkspaceDBs(ctx context.Context) ([]string, error) {
	return veridianListOrphanWorkspaceDBs(ctx, r.systemDB, r.dbConfig.Prefix)
}

// VeridianForceDropDatabaseByName (MÉTHODE) DROP une base par son NOM physique
// (utilisé par le GC orphelin qui part de pg_database). FORCE + idempotent.
func (r *workspaceRepository) VeridianForceDropDatabaseByName(ctx context.Context, dbName string, log logger.Logger) error {
	return veridianForceDropByDBName(ctx, r.systemDB, dbName, log)
}

// VeridianWorkspaceDBPrefix expose le préfixe de base configuré (pour que le GC
// puisse dériver un workspaceID d'un nom de base et appliquer les safety prefixes).
func (r *workspaceRepository) VeridianWorkspaceDBPrefix() string {
	return r.dbConfig.Prefix
}

// VeridianDeleteWorkspaceSystemRecord supprime les ROWS SYSTÈME d'un workspace
// (user_workspaces + workspace_invitations + workspaces) SANS toucher à la base
// physique du workspace. C'est l'opération-clé du wipe « record-first ».
//
// POURQUOI (bug 2026-06-19, ticket wipe-recree-base-workspace-record-system-survit) :
// le Delete upstream (workspace_postgres.go:250) fait DROP DATABASE D'ABORD, puis
// supprime les records. Quand le DROP rate sur la race « being accessed by other
// users » (le worker round-robin rouvre une connexion entre terminate et DROP), il
// return TÔT → le record `workspaces` SURVIT. Or le worker élit les workspaces via
// `List()` = `SELECT ... FROM workspaces` : tant que le record vit, le worker
// continue d'élire le ws mort, et une de ses tasks segment-queue EN VOL RECRÉE la
// base (init.go) dans la même seconde. Le force-drop n'est donc jamais la dernière
// opération sur la base.
//
// Le fix coupe la SOURCE de ré-élection : on supprime le record system EN PREMIER
// (cette opération vit sur la base SYSTÈME, totalement indépendante de la base
// workspace → elle n'est PAS bloquée par la race sur la base workspace), PUIS on
// force-drop la base en DERNIER (plus rien ne peut la recréer).
//
// Idempotent : 0 row supprimée n'est PAS une erreur (record déjà absent = OK). Les
// 3 DELETE sont indépendants ; une erreur sur l'un est remontée (le caller la traite
// best-effort). Pas de transaction : chaque DELETE est atomique côté Postgres, et on
// veut que la suppression de `workspaces` (la seule qui coupe la ré-élection worker)
// parte même si un DELETE annexe échoue → on tente les 3 et on remonte la 1ère erreur.
func (r *workspaceRepository) VeridianDeleteWorkspaceSystemRecord(ctx context.Context, workspaceID string) error {
	if r.systemDB == nil {
		return fmt.Errorf("veridian delete system record: nil systemDB")
	}
	// 1. workspaces EN PREMIER : c'est CE record que `List()` lit pour l'élection
	//    worker → le supprimer coupe immédiatement la ré-élection et donc la
	//    recréation de la base. Les rows annexes (user_workspaces / invitations)
	//    sont nettoyées juste après pour ne pas laisser de dangling FK-less rows.
	if _, err := r.systemDB.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete workspace record: %w", err)
	}
	if _, err := r.systemDB.ExecContext(ctx, `DELETE FROM user_workspaces WHERE workspace_id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete user_workspaces: %w", err)
	}
	if _, err := r.systemDB.ExecContext(ctx, `DELETE FROM workspace_invitations WHERE workspace_id = $1`, workspaceID); err != nil {
		return fmt.Errorf("delete workspace_invitations: %w", err)
	}
	return nil
}

// VeridianWorkspaceDBExists indique si la base physique d'un workspace est encore
// présente dans pg_database. Sert au wipe à confirmer qu'un DROP FORCE de
// rattrapage a réellement supprimé la base quand le DROP upstream a raté sur la
// race ("being accessed by other users"). Best-effort : une erreur de requête
// renvoie (false, err) — le caller décide (le wipe traite err comme "incertain").
func (r *workspaceRepository) VeridianWorkspaceDBExists(ctx context.Context, workspaceID string) (bool, error) {
	dbName := VeridianWorkspaceDBName(r.dbConfig.Prefix, workspaceID)
	var exists bool
	err := r.systemDB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists)
	return exists, err
}

// veridianForceDropByDBName est le coeur : DROP par nom de base déjà construit.
//
// systemDB = connexion à la base SYSTÈME, seule autorisée à DROP une autre base.
// Best-effort idempotent :
//   - base déjà absente (IF EXISTS) → succès silencieux ;
//   - erreur revoke/terminate → logguée, on continue (le FORCE du DROP termine
//     lui-même les backends) ;
//   - retourne une erreur UNIQUEMENT si le DROP final échoue.
//
// ⚠️ Séquentiel par construction : ne JAMAIS appeler en rafale parallèle sur des
// goroutines actives (worker round-robin) — un DROP DATABASE prend un lock
// exclusif ; des appels concurrents saturent/crashent le container (piège vécu
// include_orphans:true en batch).
func veridianForceDropByDBName(ctx context.Context, systemDB *sql.DB, dbName string, log logger.Logger) error {
	if systemDB == nil {
		return fmt.Errorf("veridian force drop: nil systemDB")
	}
	if !veridianSafeDBIdent.MatchString(dbName) {
		return fmt.Errorf("veridian force drop: unsafe db identifier %q (refused)", dbName)
	}

	// 1. REVOKE CONNECT : empêche toute NOUVELLE connexion. Best-effort.
	revoke := fmt.Sprintf("REVOKE CONNECT ON DATABASE %s FROM PUBLIC", dbName)
	if _, err := systemDB.ExecContext(ctx, revoke); err != nil {
		if log != nil {
			log.WithFields(map[string]interface{}{"db": dbName, "error": err.Error()}).
				Debug("veridian force drop: revoke connect failed (continuing)")
		}
	}

	// 2. Terminer les backends existants. Best-effort (le FORCE le refera).
	terminate := fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = '%s' AND pid <> pg_backend_pid()`, dbName)
	if _, err := systemDB.ExecContext(ctx, terminate); err != nil {
		if log != nil {
			log.WithFields(map[string]interface{}{"db": dbName, "error": err.Error()}).
				Debug("veridian force drop: terminate backends failed (continuing)")
		}
	}

	// 3. DROP DATABASE ... WITH (FORCE). PG13+ : termine atomiquement les backends
	//    restants → pas de race "being accessed by other users". IF EXISTS → idempotent.
	drop := fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbName)
	if _, err := systemDB.ExecContext(ctx, drop); err != nil {
		return fmt.Errorf("drop database %s with force: %w", dbName, err)
	}

	if log != nil {
		log.WithField("db", dbName).Info("veridian force drop: database dropped")
	}
	return nil
}

// veridianListOrphanWorkspaceDBs retourne les noms de bases physiques
// notifuse_ws_* présentes dans pg_database SANS record correspondant dans la
// table system `workspaces`. Correspondance reproduisant la convention upstream
// (record id avec tirets → base avec underscores). Différence calculée en Go
// (pg_database est un catalogue global, non joignable à `workspaces`).
//
// systemDB DOIT être la connexion à la base system (où vit la table workspaces).
func veridianListOrphanWorkspaceDBs(ctx context.Context, systemDB *sql.DB, prefix string) ([]string, error) {
	if systemDB == nil {
		return nil, fmt.Errorf("veridian list orphans: nil systemDB")
	}

	wsPrefix := fmt.Sprintf("%s_ws_", prefix)

	physical, err := veridianQueryStrings(ctx, systemDB,
		`SELECT datname FROM pg_database WHERE datname LIKE $1 ORDER BY datname`, wsPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list physical workspace dbs: %w", err)
	}

	recordIDs, err := veridianQueryStrings(ctx, systemDB, `SELECT id FROM workspaces`)
	if err != nil {
		return nil, fmt.Errorf("list workspace records: %w", err)
	}
	expected := make(map[string]struct{}, len(recordIDs))
	for _, id := range recordIDs {
		expected[VeridianWorkspaceDBName(prefix, id)] = struct{}{}
	}

	orphans := make([]string, 0)
	for _, db := range physical {
		if _, ok := expected[db]; !ok {
			orphans = append(orphans, db)
		}
	}
	return orphans, nil
}

// veridianQueryStrings exécute une requête mono-colonne texte et collecte tout.
func veridianQueryStrings(ctx context.Context, db *sql.DB, query string, args ...interface{}) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
