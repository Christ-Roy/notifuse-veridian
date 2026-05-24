# Test flaky `chaos-provisioning :63` — 5 provisions concurrentes sature pool DB

> **Sévérité** : 🟢 P2 — flaky, pollue rapports CI
> **Owner** : agent Notifuse (QA)
> **Créé** : 2026-05-24

## Symptôme

`tests/e2e-veridian/specs/chaos-provisioning.spec.ts:63` :

```
"5 provisions concurrentes meme tenant → 1 created, 4 idempotent, 0 erreur"
```

Fait 5 `POST /api/tenants/provision` simultanées. Le pool DB Postgres staging
ne peut pas allouer 5 nouvelles `notifuse_ws_*` databases en parallèle si déjà
sous charge → 4/5 retournent `500 "failed to create workspace database"`.

Vu **2 fois la même journée** (2026-05-23) en CI. La cause racine est :
- Soit le pool DB staging trop petit (cf. ticket
  `2026-05-24-staging-db-pool-orphan-cleanup-auto.md`)
- Soit le test est trop strict (vise 0 erreur, mais la cible business est
  "majorité d'idempotents, le code ne plante pas le serveur" — ≠ "0 erreur")

## Demande

Soit (A) **softer l'assertion** : accepter 1-2 erreurs sur 5 si elles sont
toutes "DB pool saturated" (et non panic Go), soit (B) **serialiser** les 5
provisions (mais ça change la nature du test "concurrent").

## Reco

**Option A** : assertion plus tolérante :

```ts
expect(created).toBeGreaterThanOrEqual(1);
expect(idempotents).toBeGreaterThanOrEqual(2);
expect(errors.filter(e => !e.includes('pool')).length).toBe(0);
```

Logique métier : tant qu'on a **au moins 1 created + au moins 2 idempotents
+ 0 panic Go**, on a prouvé la concurrence safe — les éventuelles erreurs
"pool saturated" sont attendues sous charge.

## Travail

1. Patcher la spec
2. Re-run CI 3 fois pour vérifier 0 flaky
3. Commit

## Estimation

30 min.

## Lien

Dépend partiellement de
`todo/2026-05-24-staging-db-pool-orphan-cleanup-auto.md` (qui réduit la
fréquence d'occurrence mais ne supprime pas le risque sous charge).
