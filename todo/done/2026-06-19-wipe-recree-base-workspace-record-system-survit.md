# Wipe test-tenant : le force-drop droppe la base, mais le worker la RECRÉE (record system survit)

> **Sévérité** : 🟡 P1 (hygiène staging — PAS un gate cold de sécurité ; les 11/11 gates anti-cramage tiennent)
> **Owner** : agent notifuse
> **Créé** : 2026-06-19
> **Trouvé par** : audit QA on-premise garde-fous cold (rejouage harness J+1 v54.0-veridian.3bbc1cce)

## Symptôme

Après `POST /api/veridian/admin/wipe-test-tenants {tenant_ids:["gfcheck866323"]}`
(réponse `{"wiped":["gfcheck866323"]}`, log `WipeTestTenants completed wiped:1`),
la base physique `notifuse_ws_gfcheck866323` **subsiste** ET le record
`notifuse_system.workspaces` **subsiste** — alors que le force-drop d'hier
(commits `8e9dcf84` / `c3550756`) est censé garantir la disparition de la base.

Vérifié en réel staging :
- `SELECT oid,datname FROM pg_database WHERE datname='notifuse_ws_gfcheck866323'`
  → **oid=6409385** (très récent vs `notifuse_system`=16384) = base **DROPPÉE puis RECRÉÉE**.
- base recréée = **25 tables** (schéma workspace réinitialisé à neuf par `init.go`, pas une restauration).
- record `notifuse_system.workspaces` toujours présent (id=`gfcheck866323`).
- worker logue ENCORE le ws ~12×/90s après le wipe → re-traitement en boucle.

## Cause racine (chronologie loggée, tout à 10:58:08Z)

```
1. DeleteWorkspace → "pq: database … is being accessed by other users"  (race worker)
2. veridian force drop: database dropped                                 (force-drop OK → base droppée)
3. Failed to process contact segment queue batch: … database … does not exist
   → une task segment-queue EN VOL recrée le pool → init.go RECRÉE la base à neuf
4. veridian: upstream DROP raced (being accessed), force-drop fallback succeeded
5. webhook tenant.deleted émis
```

Le force-drop d'hier traite le **symptôme** (la base) mais **pas la cause** : le
**record `notifuse_system.workspaces` n'est jamais supprimé** par le wipe, donc le
worker round-robin continue d'élire le ws, ses tasks segment-queue en vol
**recréent la base** dans la même seconde que le drop. Le force-drop n'est pas la
DERNIÈRE opération sur cette base.

(NB : la memory `project_batterie_garde_fous_cold` notait déjà
`wipe-test-tenants supprime le record tenant mais NE DROP PAS la base` ; ici c'est
le complément : le force-drop droppe la base mais le record survit → re-création.
Le record n'est en réalité PAS supprimé non plus.)

## Repro

```bash
# 1. provision + activité (broadcast en queue, ex. via cold-garde-fous.sh --keep)
# 2. wipe HMAC
curl -X POST $BASE/api/veridian/admin/wipe-test-tenants -d '{"tenant_ids":["<WID>"],...}'  # → {"wiped":[...]}
# 3. constater (race-sensible, refaire après quelques s)
psql -d notifuse_system -c "SELECT id FROM workspaces WHERE id='<WID>'"     # → 1 ligne (survit)
psql -c "SELECT oid FROM pg_database WHERE datname='notifuse_ws_<WID>'"     # → oid récent = recréée
```

## Workaround manuel validé (purge propre, record-first)

L'ordre CORRECT coupe la re-création AVANT de dropper :

```sql
-- 1. supprimer le record system D'ABORD (coupe la source de re-élection worker)
DELETE FROM workspaces      WHERE id='<WID>';            -- (notifuse_system)
DELETE FROM user_workspaces WHERE workspace_id='<WID>';  -- (notifuse_system)
-- 2. PUIS terminer les backends + DROP FORCE
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='notifuse_ws_<WID>';
DROP DATABASE IF EXISTS notifuse_ws_<WID> WITH (FORCE);
```

→ a marché du 1er coup (base=0, record=0), ce qui CONFIRME le diagnostic :
le record-first empêche la recréation.

## Piste de fix (voie propre, pour l'agent notifuse)

`wipeOneTenant` (`internal/service/veridian_service.go:1231`) doit, dans l'ordre :
1. **Supprimer/marquer le record system** (`workspaces` + `user_workspaces` +
   `plan` via `planRepo`) AVANT le DROP, pour que le round-robin cesse d'élire le ws.
2. Idéalement **désenregistrer/quiescer le ws du worker** (ou attendre la fin des
   tasks en vol) pour qu'aucune task segment-queue ne recrée le pool entre le
   record-delete et le DROP.
3. PUIS force-drop la base (déjà en place).
4. Optionnel : ré-asserter `workspaceDBStillExists==false` APRÈS un court délai
   (la recréation observée est intra-seconde) pour ne pas mentir dans `{"wiped":[]}`.

Sinon le starvation de bases orphelines (233→235 sur ce seul run) persiste malgré
le DROP FORCE : chaque wipe d'un ws actif laisse une base recréée derrière.

## Impact

- **Pas de risque cold/sécurité** : les 11/11 garde-fous tiennent (audit 2026-06-19).
- **Hygiène staging** : starvation lente de bases `notifuse_ws_*` (round-robin worker
  ralenti, throttle E2E flaky — déjà tracé `todo/2026-06-17-orphan-workspaces-…`).
  Ce ticket en identifie une cause active supplémentaire (recréation post-wipe).
