# Provisioning concurrent → 500 opaque `internal error` (bloque e2e-staging)

> **Sévérité** : 🟡 P1 (bloque l'auto-promo CI prod ; prod non impactée)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-07-16

## Symptôme
`tests/e2e-veridian/specs/chaos-provisioning.spec.ts:91` échoue de façon
récurrente en CI (`e2e-staging`, 1 failed / 285 passed sur 3+ runs) → gate
BLOQUANT → `deploy-prod` (needs e2e-staging) skippé → pas d'auto-promo prod.

Le test lance **5 `POST /api/tenants/provision` VRAIMENT concurrentes** (Promise.all)
du même tenant. Il TOLÈRE déjà toutes les erreurs de concurrence à message clair
(`isConcurrencyToleratedError` : "failed to create workspace database", "duplicate
key", "too many connections", "already exists", …). Il n'échoue QUE sur une erreur
NON tolérée. L'erreur observée :

```
[500] {"error":"internal error","code":"internal_error","message":"internal error"}
```

= un **500 OPAQUE** sans message identifiable. Le test le refuse à raison (invariant
A : "zéro panic Go / zéro erreur non-wrappée").

## Ce que ce N'EST PAS
- ❌ Pas la RAM DB staging : bumpé 256→512 Mo le 2026-07-16, **le flaky persiste**.
- ❌ Pas reproductible en isolation (5 provisions concurrentes à la main → 0×500) :
  n'apparaît que sous la charge parallèle des 298 tests e2e (contention DB).

## Cause probable
Sous forte concurrence (race `CreateDatabase` DB-per-workspace + contention pool),
une erreur remonte **non-wrappée** jusqu'au handler → `WriteJSONError` générique
`internal error` au lieu d'un message de concurrence tolérable. Soit :
- une branche d'erreur du provisioning concurrent ne wrappe pas le message postgres
  (race `CREATE DATABASE` / `pg_terminate` / init schema), soit
- un `recover()` de panic qui masque le vrai message en `internal error`.

## Fix propre (à faire à froid, hot path provisioning = sensible)
1. Reproduire en CI + capturer le **vrai** message via les logs du worker/service
   au moment du 500 (pas le message client masqué).
2. Wrapper l'erreur de concurrence identifiée avec un message clair (rejoint la
   liste tolérée) OU la gérer idempotemment (retry/409).
3. Ne PAS masquer en ajoutant "internal error" à la liste tolérée du test (ça
   tuerait la valeur du test — il doit continuer à détecter les vrais panics).

## En attendant
- La prod se déploie **manuellement** (canon bastion) — c'est ce qui a été fait
  pour le fix CVE Go le 2026-07-16. L'auto-promo reste bloquée par ce gate.
- Options si on veut débloquer vite : réduire la parallélisation Playwright sur ce
  spec (isoler `chaos-provisioning` en `serial`/workers=1) → traite la contention
  sans masquer le bug. À évaluer.
