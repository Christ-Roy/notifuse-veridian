# DROP DATABASE WITH (FORCE) — wipe orphelin + GC bases workspace orphelines (staging, 2026-06-18)

Le DROP de base workspace upstream (`workspaceRepository.DeleteDatabase`) fait
`DROP DATABASE IF EXISTS` **sans `WITH (FORCE)`**. Sur staging, le worker
`EmailQueueWorker` poll tous les workspaces en round-robin et rouvre une connexion
entre le `pg_terminate_backend` et le `DROP` → `database is being accessed by other
users` → DROP raté → base orpheline. Observé : 811 bases `notifuse_ws_*` pour 108
records = 703 orphelines. Spec : `todo/done/2026-06-17-orphan-workspaces-staging-db-starvation.md`.
Prouvé à la racine (psql staging) : DROP nu échoue avec 1 backend actif, DROP FORCE
réussit.

- **Fichiers veridian** : `internal/repository/veridian_workspace_drop.go` (méthodes
  sur `workspaceRepository` : `VeridianForceDropDatabase` = REVOKE CONNECT +
  terminate best-effort + `DROP DATABASE ... WITH (FORCE)`, idempotent, identifiant
  sanitize regex `[a-zA-Z0-9_]` ; `VeridianListOrphanWorkspaceDBs` = pg_database moins
  records `workspaces`, diff en Go car catalogue global non joignable ;
  `VeridianForceDropDatabaseByName` ; `VeridianWorkspaceDBPrefix`),
  `internal/service/veridian_workspace_db_cleanup.go` (DROP FORCE de rattrapage
  dans `wipeOneTenant`, `ConfigureWorkspaceDBCleanup(prefix)`),
  `internal/service/veridian_orphan_db_gc.go` (`VeridianGCOrphanWorkspaceDBs` :
  liste + DROP SÉQUENTIEL, exclut `defaultSafetyClientPrefixes` dont `canary`, cap +
  dry_run), `internal/http/veridian_orphan_db_gc_handler.go` (endpoint).
- **Pas d'import `repository` depuis `service`** (cycle `automation_postgres→service`) :
  la capacité DROP du repo est détectée par **type-assertion** sur une interface
  étroite (`veridianForceDropper` / `veridianOrphanDBGCRepo`). Zéro patch upstream.
- **Wipe (Partie A)** : `wipeOneTenant` DROP FORCE en rattrapage APRÈS
  `DeleteWorkspace` → couvre la race ET le cas record-absent/base-restante. Câblé
  via `ConfigureWorkspaceDBCleanup(config.Database.Prefix)` dans app.go (actif
  partout, jamais destructif sur une base à record). Best-effort.
- **GC (Partie B)** : endpoint **STAGING-ONLY** (503 hors staging, garde-fou comme
  cold-simulate) `POST`+`GET /api/veridian/admin/gc-orphan-workspace-dbs` (HMAC,
  POST+GET routés = anti-catchall). DROP **SÉQUENTIEL** (jamais en rafale = piège
  crash container vécu `include_orphans:true` en batch), exclut canary + clients
  réels. Validé E2E réel : 811→153 en 2 passes (838 DROP, 0 erreur, container
  healthy, 3 canary intactes). Wipe prouvé : tenant jetable → base ABSENTE de
  pg_database après wipe.
- **Partie C (TTL)** : le cron `tst*` existant profite du DROP FORCE de rattrapage.
  Reste-à-faire (ticket de suite si l'accumulation reprend) : un sweep cron
  staging-only appelant `VeridianGCOrphanWorkspaceDBs` pour les orphelines hors
  prefix `tst`. La cause racine étant supprimée, l'accumulation devrait ralentir.

⚠️ **Diffs INLINE supplémentaires** (DROP FORCE wipe + GC) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/veridian_service.go` | +champ `dbPrefix` sur `veridianService` ; `wipeOneTenant` : +appel `forceDropWorkspaceDBBestEffort` (DROP FORCE de rattrapage) après `DeleteWorkspace` |
| `internal/http/veridian_handler.go` | +champ `orphanDBGC` + routes `POST`/`GET /api/veridian/admin/gc-orphan-workspace-dbs` |
| `internal/app/app.go` | +`ConfigureWorkspaceDBCleanup(config.Database.Prefix)` (type-assert) + `SetOrphanDBGC(service, config.Environment)` après le câblage cold-simulate |

