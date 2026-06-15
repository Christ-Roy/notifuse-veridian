# Pricing sync : refill.pricing_cents ne décode pas (struct [2]int vs objet)

> **Sévérité** : 🟡 P1 (le cache pricing entier reste vide en prod → un changement de grille Hub ne se propage jamais à Notifuse)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-13
> **Détection** : validation terrain post-deploy 75244c62 (fix host Hub), endpoint `/api/veridian/admin/pricing-cache` prod

## Pattern : pourriture en couches

Bug exposé PAR le fix du host Hub fantôme (commit 600b7001, `hub.veridian.site`
→ `app.veridian.site`). Tant que le fetch échouait (NXDOMAIN), on n'atteignait
jamais le décodage JSON → ce 2e bug était masqué. Une fois le fetch réparé, le
décodage plante :

```
last_error: "decode pricing catalog: json: cannot unmarshal object into Go
struct field RefillCatalog.refill.pricing_cents of type [2]int"
last_success: null   catalog: ABSENT   stale: true
```

## Cause

La struct Go `RefillCatalog.PricingCents` était typée `map[string][][2]int`
(tableau de paires `[min, max]`), MAIS le vrai JSON du Hub est un tableau
d'OBJETS `{min, max, perLead}` :

```json
"pricing_cents": {
  "business": [
    {"min": 1, "max": 99, "perLead": 20},
    {"min": 50000, "max": null, "perLead": 4}
  ]
}
```

Source de vérité : `veridian-infra/shared/pricing/types.ts` (interface
`RefillTier { min; max; perLead }`) + `refill.ts`. Le dernier palier business a
`max: Infinity` côté TS → sérialisé `null` en JSON (`JSON.stringify(Infinity)`
=== `"null"`).

## Impact

Notifuse ne CONSOMME pas le refill (c'est Prospection), MAIS un échec de
décodage de N'IMPORTE quel champ vide le catalogue ENTIER (`runOnce` rejette
tout le payload) → le pricing **plans** (que Notifuse consomme) ne se peuple pas
non plus. Le service dégrade gracieusement (cache vide → fallback
`domain.DefaultPlanLimits`), donc rien ne casse, mais la sync ne marche jamais.

## Fix

`internal/service/veridian_pricing_sync.go` :
- Nouveau type `RefillTier{ Min int; Max *int; PerLead int }` (`Max *int` car
  `null` pour le palier ouvert).
- `RefillCatalog.PricingCents` : `map[string][][2]int` → `map[string][]RefillTier`.
- Fixture test `validCatalogJSON` corrigé au vrai contrat + assertions de
  décodage (dont le palier `max: null`) dans
  `TestVeridianPricingSyncService_Start_FetchesOnBootAndPopulatesCache`.

## Validation

- [ ] `go build ./...` + `go test ./internal/service` verts
- [ ] Push + CI verte (commit séparé, [risk:medium])
- [ ] Re-curl prod `/api/veridian/admin/pricing-cache` : `last_error` vide,
      `last_success_at` posé, `catalog` présent, `stale: false`.

SHA du fix : _(à compléter au push)_
