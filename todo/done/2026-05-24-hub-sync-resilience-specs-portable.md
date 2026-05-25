# Specs `hub-sync-resilience` non-portables (docker exec) — 3 tests bloqués hors runner self-hosted

> **Sévérité** : 🟡 P2 — bloque le debug E2E local
> **Owner** : agent Notifuse (QA)
> **Créé** : 2026-05-24 (mega-suite session N)

## Symptôme

`tests/e2e-veridian/specs/hub-sync-resilience.spec.ts` contient 3 tests (V39
gating 3 phases) qui exécutent :

```ts
execSync(`docker exec ${process.env.STAGING_DB_CONTAINER} psql -U postgres \
  -d notifuse_system -c "UPDATE veridian_plan SET last_hub_sync_at = ..."`);
```

→ Ne marche **QUE** depuis le runner CI self-hosted dev-pub (qui a accès au
container `notifuse-staging-db`). Depuis ma machine locale ou n'importe
quelle autre machine sans Docker Compose staging local : **3 fails systématiques**
qui polluent les rapports E2E et masquent les vrais bugs.

```
Error response from daemon: No such container: notifuse-staging-db
```

Affectent :
- `:172` `hub_sync_dead writes → 503 + Retry-After + error_code=hub_sync_dead, reads passent`
- `:213` `hub_sync_dead : /api/setup.status reste 200 (route systeme exempt)`
- `:232` `hub_sync recovery : Touch tenant → last_hub_sync_at refresh → writes repassent`

## Demande

Rendre les 3 tests **portables** : tournent depuis n'importe quelle machine
avec accès HTTPS à staging.

## Options

### A. Endpoint admin Hub pour forcer `last_hub_sync_at`

Ajouter un endpoint `POST /api/veridian/admin/touch-tenant-staleness` HMAC
qui prend `{tenant_id, age_hours}` et fait l'UPDATE direct. Les tests E2E
appellent ce endpoint au lieu de `docker exec`.

**Avantage** : portable partout, audité HMAC.
**Inconvénient** : nouvelle surface API exposée même si réservée admin.

### B. Tag `@requires-staging-shell` + skip si pas d'accès Docker

Détecter au `beforeAll` si `STAGING_DB_CONTAINER` est accessible :

```ts
test.beforeAll(async () => {
  try {
    execSync(`docker exec ${process.env.STAGING_DB_CONTAINER} echo ok`);
  } catch {
    test.skip(true, 'requires staging shell — skip on portable runs');
  }
});
```

**Avantage** : zéro impact API, les 3 tests skip propres en local + tournent
en CI.
**Inconvénient** : un développeur en local NE TESTE PAS la résilience V39
(seul le runner CI le fait). Mais ce sont des cas adversaires rares, ROI faible
de les tester partout.

### C. Sleep + horloge réelle (lente)

Provisionner un tenant à T0, attendre 73h pour atteindre `hub_sync_dead`
phase, puis tester. **Inviable** : 73h × 3 tests = 9 jours.

## Recommandation

**Option B** (skip portable) pour livrer vite. Si on a un besoin réel de
tester depuis n'importe où plus tard, Option A en évolution.

## Travail

- [ ] Implémenter le skip beforeAll dans `hub-sync-resilience.spec.ts`
- [ ] Ajouter commentaire de tête expliquant pourquoi
- [ ] Tester local : 3 tests skip propres, run continue
- [ ] Tester CI : 3 tests tournent (runner self-hosted a docker exec)

## Impact

Aujourd'hui, n'importe quel dev qui lance la suite E2E voit 3 fails alarmants
qui ne sont **pas** des régressions. Cause de panique, perte de temps debug.
Après fix : suite porte un message clair `3 skipped (requires staging shell)`.
