# 2026-05-20 — Flaky CI staging : Postgres "too many clients already"

> **Sévérité** : 🟡 P2 (CI flaky bloque les promotions auto vers prod, pas d'impact prod runtime)
> **Effort** : M (4-6h diagnostic + fix infra)
> **Découvert pendant** : ship BYO sending 2026-05-20 (5 runs CI consécutifs avec fails infra similaires)

## Symptômes

À chaque push sur `veridian`, l'E2E staging fail à cause de :

```
500 {"error":"create workspace: failed to create workspace database: failed to ping PostgreSQL server: pq: sorry, too many clients already"}
```

Pattern observé sur 5 runs consécutifs (run IDs 26167941347, 26168141736, 26169006346, 26169975614 + re-runs) :
- 60-77 tests passent (~95% success rate)
- 3-7 tests fail sur saturation DB
- Les tests qui fail sont des `provisionTenant()` appelés en burst après un `Cleanup test tenants` qui a wipé 30-40 tenants

## Cause racine probable

Le step `Cleanup test tenants` du workflow `veridian-ci.yml` appelle `/api/veridian/admin/wipe-test-tenants` sur 13+ prefixes successivement. Chaque wipe :
1. DROP DATABASE notifuse_ws_<tenant>
2. DELETE FROM veridian_plan WHERE workspace_id = <tenant>
3. DELETE FROM users WHERE id = <api_key_user>

Le pool DB Postgres staging ne tient pas la rafale (38 wipes en 30s). L'app continue à servir mais les nouveaux `CREATE DATABASE` (provision) plantent car `max_connections` est saturé.

L'E2E démarre immédiatement après le cleanup, sans warmup. La rafale de `provisionTenant()` (4-6 concurrent en début de spec) re-sature le pool encore chaud.

## Pistes de fix (par ordre de simplicité)

### 1. Augmenter `max_connections` Postgres staging (S — 30min)

Container `notifuse-staging-db` doit avoir `POSTGRES_MAX_CONNECTIONS=200` (default 100). Add to compose env.

### 2. Throttle le cleanup CI (S — 1h)

Au lieu de wiper 13 prefixes en parallèle, sérialiser avec `sleep 2` entre chaque prefix. Coût : +30s sur le step cleanup, mais 0 saturation.

### 3. Warmup pause entre cleanup et e2e (S — 30min)

`sleep 30` entre les 2 steps pour laisser le pool DB respirer.

### 4. Test isolation par worker (M — 3-4h)

Le `playwright.config.ts` est en `workers: 1` déjà. Pas pertinent.

### 5. Pool DB applicatif tuning (M — 2-3h)

L'app Notifuse a son propre pool (`DB_MAX_OPEN_CONNS`). Réduire de 50 à 20 force l'app à attendre plutôt que d'overflow le pool DB.

## Reco

**Combiner 1 + 2 + 3** :
- Bump `max_connections` à 200 côté container DB (mitigation rapide)
- Sérialiser le cleanup (root cause)
- Ajouter `sleep 30` warmup entre cleanup et e2e (filet de sécurité)

Effort total ~2-3h, devrait éliminer 90% des flakys.

## Workaround actuel

`gh run rerun --failed` rejoue les jobs failed. Souvent passe au 2e ou 3e essai. Coût : ~13min par re-run.

## Impact business

**Aucun en runtime** : la prod tourne sa propre DB, isolée de staging.
**CI inefficiente** : promotions auto staging → main → prod bloquées tant que e2e staging fail. Ship manuel via push direct sur main reste possible.

## Lien

- Memory `feedback_sqlmock_does_not_validate_postgres_types` : autre piège DB-related
- Memory `feedback_autonomous_ticket_session_pattern` : recette générale tickets→ship
