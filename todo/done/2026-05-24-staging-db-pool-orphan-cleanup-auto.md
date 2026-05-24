# Auto-cleanup orphans tenants staging (cron interne) — éviter saturation DB pool

> **Sévérité** : 🟡 P2 — saturation récurrente bloque les E2E
> **Owner** : agent Notifuse
> **Créé** : 2026-05-24 (vu pendant la mega-session — wipe manuel x2 nécessaire)

## Symptôme observé

Pendant la session 2026-05-23 :
- Lancement de 16 specs E2E (chacune provisionne 3-10 tenants `tst*`)
- Multiplié par 3-4 runs successifs (CI auto-promote + reruns + debug local)
- → **DB pool staging saturé** en quelques heures
- → Specs `chaos-provisioning` qui demandent 5 workspaces concurrents fail
  avec `create workspace: failed to create workspace database`

**Workaround appliqué** : appel manuel à
```bash
POST /api/veridian/admin/wipe-test-tenants
{"prefix":"tst","include_orphans":true,"dry_run":false}
```
→ wipe ~32 orphans, libère le pool.

## Cause racine

Les E2E créent des tenants `tst<random>` qu'elles wipent normalement dans
`afterEach`. Mais :
- Si un test plante mid-flow, le `afterEach` peut être skip → orphan
- Si le runner kill (OOM, timeout) → orphans
- Le wipe via E2E est `include_orphans:false` (pour ne pas crash le container
  selon memory [[reference_admin_tenants_listing_api]])
- → Orphans s'accumulent sans nettoyage

## Demande

Cron interne Notifuse qui wipe les tenants `tst*` orphelins **> 1h** sur
staging uniquement (jamais en prod). Idempotent, best-effort, log warn.

## Spec

### 1. Détection

```sql
SELECT workspace_id FROM workspaces
WHERE workspace_id LIKE 'tst%'
  AND created_at < NOW() - INTERVAL '1 hour'
  -- Et pas dans veridian_plan (= orphan) OU plan deleted_at > 7j
```

### 2. Cron

- Fréquence : toutes les 30 min
- Activation : si `DEPLOY_ENV=staging` uniquement (pas prod !)
- Implémentation : nouveau cron `internal/service/veridian_test_tenants_cleanup.go`
- Pattern : voir `veridian_idempotency_cleanup.go` ou `veridian_api_key_grace_cleanup.go`
  pour le template (cron + best-effort + log + métriques)

### 3. Garde-fou

- **JAMAIS** en prod (check explicit `cfg.DeployEnv == "staging"`)
- **Prefix `tst` requis** (cf. `defaultSafetyClientPrefixes` memory)
- **Max 100 wipes par tick** (rate-limit anti-runaway)
- **Log warn** chaque tick avec count + last cleanup timestamp

### 4. Bonus : endpoint metrics

`GET /api/veridian/admin/test-tenants-stats` :

```json
{
  "total_test_tenants": 47,
  "orphans_older_than_1h": 12,
  "last_auto_cleanup_at": "2026-05-24T21:00:00Z",
  "last_cleanup_wiped_count": 5
}
```

Permet à un dev/agent de monitorer la santé du pool DB staging.

## Convention

- Crée `internal/service/veridian_test_tenants_cleanup.go` + `_test.go`
- Mocks gomock v1.6.0
- Test colocalisé bloquant §1
- Pre-push standard

## Estimation

~3h (suivre pattern cron existant + tests + wire dans app.go).

## Définition de done

- [ ] Cron tourne sur staging
- [ ] Logs confirment wipes périodiques (vérif via `obs check` staging)
- [ ] Specs E2E `chaos-provisioning` cessent de fail avec saturation pool
- [ ] Prod : confirmé que le cron est **gated DeployEnv=staging** (audit code +
      vérif via `ssh prod-pub docker exec notifuse-prod env | grep DEPLOY_ENV`)
