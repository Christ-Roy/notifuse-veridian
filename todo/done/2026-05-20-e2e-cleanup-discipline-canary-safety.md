# 2026-05-20 — Discipline E2E cleanup + protection canary tenants

> **Sévérité** : 🟡 P2 (qualité/coût infra, pas bloquant business)
> **Effort** : M (3-4h)
> **Découvert pendant** : nettoyage 2026-05-20 — 50 tenants orphelins prod + 145 staging accumulés en quelques semaines

## Contexte

Le ménage du 2026-05-20 a révélé :
- **Prod** : 63 → 10 tenants après wipe (50 e2e/hubtest/loginflow/etc orphelins)
- **Staging** : 146 → 1 tenant après wipe (145 e2e/up*/regrmp*/etc orphelins)

Les E2E spec créent des tenants avec **30+ prefixes différents** (`e2e177*`, `chaos*`, `magic*`, `stat*`, `pwsus*`, `pwdel*`, `pwpath*`, `pwread*`, `smoke*`, `down*`, `unkplan*`, `magtam*`, `qgrant*`, `qgrej*`, `qgridem*`, `qgrunblk*`, `qgrresume*`, `qgrnoreason*`, `qince*`, `upe*`, `upa*`, `upb*`, `upc*`, `regrmp*`, `staging*`, `trial*`, `chrome*`, `delconf*`, `genmag*`, `paywsmoke*`, `resumee*`, `smokee*`, `sus2*`...).

Le **step `Cleanup test tenants` du CI** wipe 13 prefixes connus en batch en fin de run, mais :
1. Les autres prefixes (non listés) restent éternellement
2. La rafale de wipes sature le pool Postgres → CI flaky (ticket `2026-05-20-flaky-ci-staging-postgres-saturation.md`)

## Aussi : ajout des 3 canary tenants long-lived

Créés le 2026-05-20 sur prod et staging :

- **canaryfree** (plan_source=internal) — baseline tenant créé sur archi récente, simule un Free user
- **canarypro** (plan_source=internal) — baseline Pro user
- **canaryenterprise** (plan_source=internal) — baseline Enterprise/unlimited

Quota tous à -1 (BYO sending). plan_source=internal = immune downgrade Stripe + visible "tenant interne Veridian".

**Usage** : avant chaque promote main → prod, lancer un test E2E `canary-witness.spec.ts` qui vérifie que les requêtes critiques sur les 3 canary continuent de marcher. Si fail → ALTER COLUMN, migration foireuse, etc. détecté **avant** que le client subisse.

## Travail à faire

### 1. Convention naming E2E (S — 1h)

Tous les tests E2E doivent provisionner avec prefix `t-` (court, lisible) suivi d'un timestamp court :

```typescript
const tid = `t-${Date.now().toString(36).slice(-6)}`;
```

Refactor des specs existants pour adopter ce naming. Une seule convention = facile à wiper en CI (`prefix: "t-"`) sans risquer de toucher du légitime.

### 2. afterEach / afterAll cleanup obligatoire (S — 1-2h)

Au lieu du batch cleanup en fin de CI (qui sature le pool DB), chaque spec doit nettoyer SES tenants immédiatement :

```typescript
test.afterEach(async () => {
  if (provisioned.length > 0) {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: provisioned,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
    provisioned.length = 0;
  }
});
```

Avantages :
- Pas de batch 38 wipes en 30s → pas de saturation DB
- Tenants nettoyés au fil de l'eau → DB toujours propre
- Pas besoin du step `Cleanup test tenants` du CI workflow

### 3. Étendre `safety_client_prefixes` defaults (S — 30min)

Le default actuel (`apicalinfo`, `robinix`, `lyon`, `loyer`, `veridiansite`) protège les vrais clients. Ajouter pour le futur :

```go
// internal/domain/veridian.go ou veridian_service.go
var defaultSafetyClientPrefixes = []string{
    "apicalinfo", "robinix", "lyon", "loyer", "veridiansite",
    // === Veridian patch 2026-05-20 === Protect canary witness tenants
    "canary",
    // === Veridian patch === Tes workspaces personnels (immune accidental wipe)
    "robertbrunon", "robertstagingtest", "brunon5robert", "rbrunon", "truy",
    // Workspaces clients réels
    "antjacquet", "darysisowath", "guilhemjacquet", "ismailelmouaddab",
}
```

Le caller (CI script) peut toujours override en passant son propre `safety_client_prefixes`, mais le default est safe.

### 4. Spec canary-witness.spec.ts (M — 1-2h)

Nouveau test dans `tests/e2e-veridian/specs/canary-witness.spec.ts` :

```typescript
test.describe('Canary witness — vérifie que les vieux tenants survivent aux migrations', () => {
  for (const canary of ['canaryfree', 'canarypro', 'canaryenterprise']) {
    test(`@canary ${canary} — status endpoint répond + plan/quota cohérent`, async () => {
      const r = await hmacFetch(`/api/tenants/${canary}/status`, 'GET');
      expect(r.status).toBe(200);
      const data = await r.json();
      expect(data.plan_source).toBe('internal');
      expect(data.monthly_email_quota).toBe(-1);
      // Pas de wipe — ces tenants doivent rester !
    });
  }
});
```

Tag `@canary` pour pouvoir le runner spécifiquement avant promote prod :
```bash
npx playwright test --grep @canary
```

### 5. Documentation runbook (S — 30min)

Créer `runbooks/canary-tenants.md` expliquant :
- Pourquoi ces 3 tenants existent
- Quand les utiliser (avant migrations DB, avant changes schéma, avant durcissement quota)
- Comment NE PAS les wiper (safety prefix dans CI)
- Comment les rafraîchir si l'archi évolue trop (re-create depuis snapshot)

## Reco shipping

Ordre :
1. Étendre safety prefixes (étape 3) — 30min, prévient les accidents → ship d'abord
2. afterEach cleanup (étape 2) — 1-2h, débloque le flaky CI
3. Spec canary-witness (étape 4) — 1-2h, exploit les canary
4. Refactor naming `t-*` (étape 1) — 1h, cosmétique mais utile

Total ~4h de travail, gros impact qualité.

## Lien

- Memory `feedback_autonomous_ticket_session_pattern` (recette tickets→ship)
- Ticket `2026-05-20-flaky-ci-staging-postgres-saturation.md` (root cause adjacent)

---

## Update — 2026-05-20 — Livré partiellement (étapes 3 + 4)

### ✅ Étape 3 — safety prefixes étendus (commit `aca10de0`)

`internal/service/veridian_service.go::defaultSafetyClientPrefixes` étendu
de 5 → 15 prefixes :

- Clients réels existants : `apicalinfo`, `robinix`, `lyon`, `loyer`, `veridiansite`
- Clients réels ajoutés : `antjacquet`, `darysisowath`, `guilhemjacquet`, `ismailelmouaddab`
- Canary witness : `canary` (matche `canaryfree`, `canarypro`, `canaryenterprise`)
- Robert perso : `robertbrunon`, `robertstagingtest`, `brunon5robert`, `rbrunon`, `truy`

Tests ajoutés (Constitution §1) :
- `TestVeridianService_DefaultSafetyClientPrefixes_ContainsCriticalEntries` —
  vérifie présence des 15 prefixes attendus, 0 doublon, ≥3 chars
- `TestVeridianService_WipeTestTenants_SkipsCanaryAndClientPrefixes` —
  test fonctionnel sur 6 tids matchant safety prefixes → tous skipped, aucun wipe

### ✅ Étape 4 — Spec canary-witness `@prod-safe @canary` (commit `3b197d60`)

`tests/e2e-veridian/specs/canary-witness.spec.ts` créé. Vérifie via
`/api/tenants/:id/status` (HMAC GET, read-only) que :
- `canaryfree` plan=free actif quota=-1
- `canarypro` plan=pro actif quota=-1
- `canaryenterprise` plan=enterprise actif quota=-1

Tag `@prod-safe` → inclus AUTO dans le job `e2e-prod` après chaque promote.
Testé localement contre prod + staging : 3/3 verts.

### ⏳ Reste à faire (autre session)

- **Étape 2 — afterEach cleanup** : refactor des specs existants pour
  nettoyer chacun ses tenants au fil de l'eau. Demande de toucher 6-8 fichiers
  spec et de valider qu'on casse rien. ~2h.
- **Étape 1 — refactor naming `t-*`** : cosmétique, à faire en parallèle de
  l'étape 2 puisque ça touche les mêmes fichiers.

Ces 2 étapes restantes sont indépendantes du fix postgres et de la safety.
Le ticket reste ouvert mais P3 (qualité, pas bloquant).

---

## Update — 2026-05-21 — Étape "API admin orphelins" livrée (lot G hors ticket)

### ✅ Livré indépendamment du ticket original

Pendant un sprint de cleanup CI, j'ai trouvé que `WipeTestTenants` filtrait
les candidats UNIQUEMENT via `veridian_plan` — donc rate **tous** les
workspaces orphelins (présents dans `workspaces` mais sans plan).

**Solutions shippées** (commit Lot G 2026-05-21) :

1. `WipeTestTenantsInput.IncludeOrphans bool` — scan workspaceRepo.List en plus
2. `GET /api/veridian/admin/tenants?prefix=X&include_orphans=true` — dry-run
   listing read-only (managed vs orphans buckets)
3. `.github/workflows/veridian-ci.yml` : ajout `include_orphans:true` au body
   du step Cleanup test tenants → plus d'orphelins qui s'accumulent

**Tests colocalisés** (Constitution §1) : domain (JSON), service (12), http (7).

### ⏳ Reste à faire (toujours pertinent même après lot G)

- **Étape 1 — refactor naming `t-*`** : cosmétique, en parallèle de l'étape 2
- **Étape 2 — afterEach cleanup spec** : refactor 11 specs pour cleanup au fil
  de l'eau. Bénéfice : plus de batch wipe en fin de CI → CI plus rapide +
  pool DB toujours propre. ~2-3h.

Ces 2 étapes restantes sont du refactor frontend Playwright, indépendant du
fix backend lot G. Priorité P3 (le bug pool DB orphelins est résolu).

---

## ✅ CLÔTURE 2026-05-22 — toutes les étapes livrées

- Étape 1 (naming `tst` unifié) : ✅ Lot N + Lot N suite — 14/16 specs
  (les 2 non concernées = canary-witness + prod-safe, read-only, ne
  créent aucun tenant)
- Étape 2 (afterEach cleanup) : ✅ Lot N + Lot N suite, 11 specs créatrices
- Étape 3 (safety prefixes étendus) : ✅ livré avant (commit aca10de0)
- Étape 4 (canary-witness spec) : ✅ livré avant (commit 3b197d60)

En prod via v41.0-veridian. Ticket clos.
