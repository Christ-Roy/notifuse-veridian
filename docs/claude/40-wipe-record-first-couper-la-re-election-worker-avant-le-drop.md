# Wipe RECORD-FIRST — couper la ré-élection worker avant le DROP (staging, 2026-06-19)

Le force-drop d'hier (DROP FORCE) traitait le SYMPTÔME (la base) mais pas la CAUSE :
après un wipe, la base droppée était **RECRÉÉE dans la seconde**. Cause racine : le
`Delete` upstream (`workspace_postgres.go:250`) fait **DROP DATABASE D'ABORD**, puis
supprime le record `notifuse_system.workspaces`. Quand le DROP rate sur la race
« being accessed by other users » (le worker round-robin rouvre une connexion entre
`pg_terminate_backend` et le DROP), `Delete` **return tôt → le record `workspaces`
SURVIT**. Or le worker élit les workspaces via `List()` = `SELECT … FROM workspaces`
(`worker.go:189`) : tant que le record vit, le worker ré-élit le ws mort et une task
segment-queue EN VOL **recrée la base** (`init.go`, 25 tables, oid récent). Le
force-drop n'était donc jamais la DERNIÈRE opération. Spec : ticket
`todo/done/2026-06-19-wipe-recree-base-workspace-record-system-survit.md`. Workaround
manuel validé (record-first) confirmé en prouvant le diagnostic.

- **Fix (voie propre, record-first)** : dans `wipeOneTenant`, supprimer le **record
  système EN PREMIER** (`workspaces` puis `user_workspaces` + `workspace_invitations`),
  PUIS DROP FORCE la base **en DERNIER**. La suppression du record vit sur la base
  SYSTÈME (indépendante de la base workspace → JAMAIS bloquée par la race sur la base
  workspace) → elle coupe immédiatement la ré-élection worker → plus aucune task ne
  peut recréer la base après le force-drop.
- **Fichier veridian** : `internal/repository/veridian_workspace_drop.go`
  (`VeridianDeleteWorkspaceSystemRecord` : 3 DELETE system-DB, `workspaces` en premier,
  idempotent — 0 row = OK, nil-systemDB rejeté). Service
  `internal/service/veridian_workspace_db_cleanup.go` (capacité type-assert
  `veridianSystemRecordDeleter` + `deleteWorkspaceSystemRecordBestEffort` → retourne
  `recordCut bool`). Aucun import `repository` depuis `service` (même pattern
  type-assertion que le force-drop). **Pas de migration** (DELETE sur tables system
  existantes).
- **Garde-fou prefix/canary** : inchangé — la suppression du record n'est atteinte
  qu'à l'intérieur de `wipeOneTenant`, lui-même gardé par `defaultSafetyClientPrefixes`
  (canary + clients réels) en amont dans `WipeTestTenants`. Un record canary*/client
  réel n'arrive JAMAIS au record-first.
- **Tolérance erreur DeleteWorkspace bénigne** : si le record-first a réussi
  (`recordCut`) et que le force-drop est fait, une erreur résiduelle de
  `DeleteWorkspace` (ex. `ErrWorkspaceNotFound` car le record est déjà parti, ou auth
  owner échouée) est traitée comme bénigne (log, pas d'échec). Best-effort : record-first
  en erreur → on continue (DeleteWorkspace upstream nettoiera, ou le prochain wipe/GC).
- **Validé E2E ON-PREMISE staging** : workspace jetable AVEC activité (broadcast en
  queue → worker l'élit) → wipe → record ABSENT + base ABSENTE de pg_database + **NE
  RÉAPPARAÎT PAS après 90s** (avant : base recréée avec oid récent dans la seconde).

⚠️ **Diffs INLINE supplémentaires** (wipe record-first) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/veridian_service.go` | `wipeOneTenant` réordonné : `deleteWorkspaceSystemRecordBestEffort` (record-first) AVANT `DeleteWorkspace`, force-drop EN DERNIER ; nouveau cas `recordCut` (erreur DeleteWorkspace bénigne si record déjà coupé) |

Fichiers veridian touchés : `internal/repository/veridian_workspace_drop.go`
(`VeridianDeleteWorkspaceSystemRecord`), `internal/service/veridian_workspace_db_cleanup.go`
(`veridianSystemRecordDeleter` + `deleteWorkspaceSystemRecordBestEffort`) + tests
colocalisés.

