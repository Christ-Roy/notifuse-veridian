# 2026-05-19 — `plan_source` + immunité Stripe sur plans offerts

> **Spec** : `../CONTRAT-HUB.md` §3.3 (plans offerts) + §5.2 (update-plan)
> **Sévérité** : 🟡 P1 fonctionnel (sinon les plans offerts peuvent être écrasés par un cron Stripe)
> **Effort** : M (2-8h)

## Contexte

Contrat §3.3 distingue 5 sources de plan :
- `stripe` (paid, géré par Stripe billing)
- `manual` (Robert assigne, peut être annulé)
- `lifetime_site_vitrine` (offert via site vitrine, immutable)
- `lifetime_partner` (offert à un partenaire, immutable)
- `internal` (Veridian use)

Les 3 dernières doivent être **immunes** aux downgrades automatiques venant du Hub (qui synchronise Stripe). Sans ce flag côté Notifuse, un user qui a un `lifetime_partner` peut se voir downgrader en `free` si Stripe webhook arrive entre-temps.

## Travail

1. **Migration** : ajouter `plan_source VARCHAR(32) NOT NULL DEFAULT 'stripe'` sur `veridian_plan`.

2. **`domain.UpdatePlanInput`** : ajouter `PlanSource string` (optionnel, default `stripe` si non fourni — back-compat).

3. **`service.UpdatePlan`** :
   ```go
   existing, _ := s.planRepo.Get(ctx, tenantID)
   if existing.PlanSource == "lifetime_site_vitrine" ||
      existing.PlanSource == "lifetime_partner" ||
      existing.PlanSource == "internal" {
       if input.PlanSource != existing.PlanSource && input.PlanSource == "stripe" {
           return ErrPlanImmune  // mappé 423 Locked ou 409 plan_locked
       }
   }
   ```

4. **`UpdatePlanResponse`** : ajouter `previous_plan` (audit trail §5.2).

5. **Tests** : colocalisé service + handler. Cas : tenter de downgrader un `lifetime_partner` via Stripe → 409 `plan_locked`.

## Risque migration

P2 — colonne avec default, idempotent. Backfill manuel pour les 9 tenants existants si nécessaire (probablement tous `stripe` ou `internal` selon contexte).

## Coordination Hub

Le Hub doit envoyer `plan_source` au moment de `update-plan` (et `provision`). Sans ça, tous les plans sont marqués `stripe` par défaut → immunité jamais active.
