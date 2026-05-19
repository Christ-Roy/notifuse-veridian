# 2026-05-19 — Quotas exposés au provision (v1.2)

> **Spec** : `../CONTRAT-HUB.md` §5.17
> **Sévérité** : 🟢 P3 cosmétique
> **Effort** : S (<2h)

## Contexte

Aujourd'hui `Provision` calcule le quota via `domain.QuotaForPlan(plan)` hardcoded :
```go
// internal/domain/veridian.go
func QuotaForPlan(plan string) int64 {
    switch plan { ... }
}
```

Contrat §5.17 permet au Hub d'**envoyer le quota au provision** pour avoir une source de vérité unique (le Hub). Si le champ `quotas` est absent dans `ProvisionInput`, on tombe sur le hardcoded local (back-compat).

## Travail

1. `ProvisionInput` : ajouter `Quotas *PlanQuotas` (optionnel) où `PlanQuotas{ MonthlyEmails int64, ... }`.

2. `service.Provision` : si `input.Quotas != nil`, utiliser ces valeurs au lieu du hardcoded. Sinon fallback `QuotaForPlan(plan)`.

3. `service.UpdatePlan` : idem, ajouter `Quotas *PlanQuotas` à `UpdatePlanInput`.

4. Tests colocalisés.

## Risque

P0 — feature opt-in. Pas de breaking change.

## Reco

Petit ticket à shipper en passant si on touche le contrat. Sinon, attendre que le Hub décide de centraliser les quotas.
