# Consommer Hub /api/pricing/plans (JSON canonique)

> **Sévérité** : 🟢 P2
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-05-21
> **Demandeur** : agent veridian-infra (création shared/ + endpoint Hub)

## Contexte

Robert a créé un **dossier shared cross-app** dans `veridian-infra/shared/`
qui héberge le **catalogue pricing canonique** (11 plans Veridian, refill
leads dégressif, annual perks) en TypeScript.

Les apps TypeScript (Hub, Prospection) consomment ce shared via Git submodule.
Notifuse est en Go → ne peut pas importer directement. Pour rester cohérent
sans dupliquer les valeurs en dur dans `internal/domain/veridian.go`, le
Hub expose le catalogue en JSON :

```
GET https://hub.veridian.site/api/pricing/plans
Cache-Control: public, max-age=3600, stale-while-revalidate=86400
```

## Shape JSON exposée

```json
{
  "plans": {
    "notifuse-pro": {
      "id": "notifuse-pro",
      "name": "Notifuse Pro",
      "tier": "pro",
      "apps_unlocked": ["notifuse"],
      "price_eur": 29,
      "price_eur_yearly_per_month": 24,
      "stripePriceIdLive": { "month": null, "year": null },
      "stripePriceIdTest": { "month": null, "year": null },
      "welcome_leads": 0,
      "seats": null,
      "features": ["notifuse_ab_testing"],
      "annual_perks": true,
      "plan_source": "stripe",
      "hidden_from_public": false,
      "rank": 2
    },
    ...
  },
  "refill": {
    "pricing_cents": { "freemium": [...], "pro": [...], "business": [...] },
    "max_per_order": 100000
  },
  "annual_perks": {
    "supportPriority": true,
    "onboardingSession": { "durationMin": 60, "deliveredVia": "visio" },
    "premiumTutos": true,
    "annualDiscountPct": 17
  },
  "version": "0.1.0",
  "generated_at": "2026-05-21T..."
}
```

## Demande

1. Ajouter un **client Go** qui fetche le catalogue au boot (avec cache local
   TTL 1h + fallback sur les valeurs par défaut hardcodées dans
   `internal/domain/veridian.go` si Hub down).
2. Mapper les `tier` Notifuse (`free`/`pro`/`business`) aux clés canoniques
   (`notifuse-free`/`notifuse-pro`/`notifuse-business`).
3. Lire `features` côté Notifuse pour gater white-label (`notifuse_white_label`)
   au lieu du flag legacy `feature_white_label` dans la DB.
4. Bonus si possible : `validate-pricing` script Go en CI qui fail si le
   JSON Hub est incohérent avec les valeurs hardcodées (drift detection).

## Pourquoi P2 et pas P1

Notifuse fonctionne déjà avec ses valeurs hardcodées V37. Le risque
"dérive cross-app" est faible court-terme car les prix sont stables.
À traiter quand l'agent Notifuse aura un sprint capacity, ou avant le
prochain pivot pricing pour éviter un nouveau bug de désync.

## Garde-fous

- Le endpoint Hub est **public + cacheable** (CDN 1h) → pas de secret
- Si Hub down : Notifuse continue avec ses valeurs hardcodées
- Cache TTL local 1h évite de trop bouffer le Hub
- Pas besoin de HMAC (lecture publique du catalogue)

## Références

- Endpoint Hub : `veridian-hub/app/api/pricing/plans/route.ts`
- Source de vérité : `veridian-infra/shared/pricing/plans.ts`
- Doc humaine : `veridian-hub/docs/PRICING-VERIDIAN.md` v1.1
- Memory shared submodule : à créer par l'agent qui ouvre ce ticket
