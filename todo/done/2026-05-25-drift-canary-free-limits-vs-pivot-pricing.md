# [NOTIFUSE] Drift canaryfree.limits vs pivot pricing 2026-05-21

> **Sévérité** : 🟡 P2 — pas bloquant prod mais incohérence canary vs code
> **Owner** : agent Notifuse
> **Créé** : 2026-05-25 par team-lead vague 4
> **Découvert via** : MEGA-05 canary witness verification spec (par agent-mega-e2e-notifuse vague 4)

## Symptôme

Le canary tenant `canaryfree` en staging (et probablement prod) retourne une shape `/api/tenants/canaryfree/limits` qui CONTREDIT le pivot pricing 2026-05-21 :

```json
// Actuel canaryfree
{
  "limits": {
    "FeatureABTesting": false,    // ← pivot dit true (gratuit pour tous)
    "MaxContacts": 500,            // ← pivot dit -1 (illimité)
    "MaxSeats": 1,                 // ← pivot dit -1
    "MaxOAuthAccounts": 1,         // ← pivot dit -1
    "MaxActiveSequences": 1,       // ← pivot dit -1
    ...
  }
}
```

**Référence code** : `internal/domain/veridian.go::DefaultPlanLimits["free"]` (vérifié par `TestDefaultPlanLimits_FreeAllUnlimited`) → toutes valeurs à `-1` ou `true` sauf `FeatureWhiteLabel`.

**Fresh provision** : un tenant fraîchement provisionné en plan=free retourne BIEN la shape post-pivot (vérifié dans MEGA-01 lifecycle). Donc le bug est sur les tenants **anciens** qui ont leur snapshot V37 figé pré-pivot.

## Hypothèses (à creuser)

### A) Canary jamais re-snapshoté
Les canary tenants ont été créés avant le pivot 2026-05-21. Leur ligne `veridian_plan` contient les anciennes valeurs (MaxContacts=500, FeatureABTesting=false). Le `Upsert` du repo NE met PAS à jour les dimensions V37 sur conflict (`isZeroPricingDimensions` check). Donc tant qu'on ne refait pas un `UpdatePlan`, ils restent figés.

→ Fix : 1 appel `update-plan plan=free plan_source=internal` sur les 3 canary pour réécrire les dimensions via `LimitsForPlan(plan)` (cf. `UpdatePlan` repo lignes 267-306).

### B) GetLimits handler a un chemin spécial plan_source=internal
Si le service `GetLimits` lit la ligne DB telle quelle (sans appliquer `LimitsForPlan` à la volée), alors tant que la DB contient les vieilles valeurs, le client voit les vieilles valeurs. C'est probablement le cas — le pattern d'optimisation classique.

→ Confirmer en lisant `internal/service/veridian_service.go::GetLimits` (si existe) ou `internal/http/veridian_handler.go::handleLimits`.

## Impact business

- Si Robert montre canaryfree à un client en demo, il verra FeatureABTesting=false alors que le pivot dit "A/B gratuit pour tous" → **promesse trahie visible**
- Le test MEGA-05 a dû relâcher son assertion pour éviter le faux positif → **anti-régression dégradée**

## Demande

1. **Diagnostic** : confirmer hypothèse A vs B en lisant le code GetLimits
2. **Fix** :
   - Si A : script one-shot qui call `update-plan plan=free plan_source=internal` sur les 3 canary (canaryfree, canarypro, canaryenterprise) staging + prod
   - Si B : ajouter `LimitsForPlan(plan)` à la volée dans GetLimits (override la DB par les defaults code-source-of-truth)
3. **Tests anti-régression** :
   - Réactiver l'assertion FeatureABTesting=true dans MEGA-05
   - Ajouter un test unit `TestGetLimits_FreeReturnsPostPivotShape` qui crée un tenant DB avec vieilles valeurs (MaxContacts=500) puis vérifie que /limits retourne les valeurs post-pivot (-1)

## DoD

- [x] Hypothèse confirmée (A ou B)
- [x] Fix appliqué
- [x] canaryfree staging + prod retournent la shape post-pivot
- [x] MEGA-05 spec re-renforcée (assertion FeatureABTesting=true)
- [x] Ticket archivé dans done/ avec ## Réponse

## Référence

- Pivot pricing source de vérité : `../veridian-hub/docs/PRICING-VERIDIAN.md`
- Memory pivot : `project_pricing_pivot_2026_05_21.md`
- Spec qui a découvert : `tests/e2e-veridian/specs/mega/05-canary-witness-verification.spec.ts`

---

## Réponse — 2026-05-25 (agent canary-drift-fix, vague 5)

### Diagnostic — Hypothèse A confirmée (cause racine data)

Les deux hypothèses A et B sont vraies mais composées :

- **A (cause racine)** : `Upsert` ne réécrit PAS les dimensions V37 sur `ON CONFLICT` (cf. commentaire `veridian_plan_postgres.go:143-146` : "ne pas régresser silencieusement un tenant pro vers free"). Donc les canary créés avant le pivot 2026-05-21 ont gardé leur snapshot V37 pré-pivot (`MaxContacts=500`, `FeatureABTesting=false`...).
- **B (mécanisme amplifiant)** : `GetLimits` retourne les valeurs DB telles quelles (`veridian_service.go:1696-1736`), avec un fallback `isEmptyLimits` qui ne déclenche QUE si toutes les dimensions sont à zéro/false. Pour `canaryfree`, `MaxContacts=500` ≠ 0 → fallback inactif → drift exposé.

Le fallback B était intentionnel (cas `tenant pré-V37 jamais re-upsert`) mais ne couvre pas le cas `tenant pré-pivot avec dimensions non-nulles legacy`.

### Décision — Data fix (pas de patch service)

Choix : approche A (data fix) plutôt que B (override `LimitsForPlan` à la volée dans `GetLimits`), parce que :
- Approche B casserait les **enterprise deals hors-grille** (Robert peut faire un Upsert direct avec quotas custom pour un client — c'est le pattern documenté `veridian_plan_postgres.go:253-266`).
- Approche A est ciblée, sûre, et la source de vérité reste `domain.LimitsForPlan` appliqué via `UpdatePlan`.

### Fix appliqué — 6 calls `update-plan plan_source=internal`

Script bash one-shot avec HMAC signe sur les 3 canary × 2 envs (staging + prod). `update-plan` call → service `UpdatePlan` → repo `UpdatePlan` qui écrase TOUTES les dimensions V37 via `LimitsForPlan(plan)` (lignes 267-306). `plan_source=internal` préservé (immune downgrade Stripe maintenue).

**Validation post-fix (staging + prod)** :

| Canary | plan | MaxContacts | ABTest | Branding | WhiteLabel | HistRet |
|---|---|---|---|---|---|---|
| canaryfree | free | -1 | true | true | false | -1 |
| canarypro | pro | -1 | true | true | false | -1 |
| canaryenterprise | enterprise | -1 | true | true | true | -1 |

Tous conformes au pivot 2026-05-21. Differenciation business+ (`WhiteLabel=true` enterprise uniquement) respectée.

### Anti-régression — Spec MEGA-05 renforcée

`tests/e2e-veridian/specs/mega/05-canary-witness-verification.spec.ts` ré-asserte désormais :
- `MaxContacts == -1`, `MaxSeats == -1`, `MaxOAuthAccounts == -1`, `MaxCustomDomains == -1`, `MaxActiveSequences == -1`, `HistoryRetentionDays == -1`
- `FeatureABTesting == true`, `FeatureBrandingRemoved == true`
- `FeatureWhiteLabel == canary.whiteLabel` (free/pro=false, enterprise=true)
- Plus la note "drift documenté", remplacée par référence au pivot + ce ticket.

Spec testée localement contre staging (3/3 ✓ en 2.8s) et prod (3/3 ✓ en 2.5s).

Comme la spec est tag `@prod-safe @canary`, elle joue automatiquement à chaque promote main → prod, donc tout futur drift sera bloqué au e2e-prod-smoke.

### Pas de test unit Go ajouté

Le DoD suggérait `TestGetLimits_FreeReturnsPostPivotShape` mais ce test n'a pas de sens dans l'approche A : `GetLimits` lit la DB telle quelle (comportement documenté et voulu, cf. enterprise custom). Les tests qui couvrent le contrat sont déjà :
- `TestDefaultPlanLimits_FreeAllUnlimited` (domain) — pivot source de vérité
- `TestVeridianPlanRepository_UpdatePlan_*` (repo) — réécriture dimensions
- MEGA-05 e2e (intégration bout en bout sur tenants réels) — anti-régression durable

Ajouter un test sqlmock qui mock un row drift et assert que `GetLimits` retourne... le row drift... aurait juste validé le comportement DB-as-source-of-truth, déjà testé indirectement.

### Fichiers modifiés

- `tests/e2e-veridian/specs/mega/05-canary-witness-verification.spec.ts` — assertions pivot renforcées
- 6 rows DB `veridian_plan` réécrits via API admin HMAC (staging+prod) — pas de migration

Aucun code Go touché.
