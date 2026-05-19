# 2026-05-19 — Lifecycle endpoints : soft-delete / restore / purge / touch / usage-summary

> **Spec** : `../CONTRAT-HUB.md` §5.7 (machine à états) + §5.8 (endpoints)
> **Sévérité** : 🟡 P1 fonctionnel
> **Effort** : L (1-2 jours)

## Contexte

Contrat v1.1 a introduit une machine à états explicite sur le cycle de vie tenant :

```
active ──suspend──> suspended ──resume──> active
active ──soft-delete──> soft_deleted ──restore──> active
soft_deleted ──30j──> purge_eligible ──purge──> purged (hard delete)
active ──touch──> active (anti-soft-delete par cron)
```

Notifuse implémente actuellement uniquement `suspend/resume` et un `DELETE /api/tenants/{id}` mappé sur `SoftDelete()`. **Manque** :

## Endpoints à créer

| Méthode | Path | Body | Réponse |
|---|---|---|---|
| `POST` | `/api/tenants/{id}/soft-delete` | `{reason: string}` | `{tenant_id, status: "soft_deleted", deleted_at, purge_eligible_at}` |
| `POST` | `/api/tenants/{id}/restore` | `{reason: string}` | `{tenant_id, status: "active", restored_at}` |
| `POST` | `/api/tenants/{id}/purge` | `{reason: string, confirm: "PURGE"}` | `{tenant_id, status: "purged", purged_at}` (HARD DELETE) |
| `POST` | `/api/tenants/{id}/touch` | `{}` | `{tenant_id, touched_at, soft_delete_eligible_at}` |
| `GET` | `/api/tenants/{id}/usage-summary` | — | `{tenant_id, messages_sent_30d, last_activity_at, contacts_count, ...}` |

## Travail à faire

1. **Migration DB** : ajouter colonnes à `veridian_plan` :
   - `restored_at TIMESTAMP NULL` (audit trail)
   - `purge_eligible_at TIMESTAMP NULL` (calculé `deleted_at + 30j`)
   - `last_touched_at TIMESTAMP NULL` (anti-soft-delete cron)
   - `lifecycle_reason TEXT` (audit GDPR)

2. **Service `veridian_service.go`** :
   - `SoftDelete(ctx, tenantID, reason)` → set `deleted_at`, `purge_eligible_at = deleted_at + 30d`, emit `tenant.soft_deleted`
   - `Restore(ctx, tenantID, reason)` → clear `deleted_at` + `purge_eligible_at`, set `restored_at`, emit `tenant.restored`
   - `Purge(ctx, tenantID, reason)` → DROP workspace DB + DELETE plan row, emit `tenant.purged`. **Refuser si `purge_eligible_at > NOW()`** (sauf override Robert).
   - `Touch(ctx, tenantID)` → update `last_touched_at = NOW()`. Debouncing : si touché dans les dernières 24h, no-op.
   - `UsageSummary(ctx, tenantID)` → agréger `message_history` 30j + lookup workspace contacts.

3. **Handlers `veridian_handler.go`** :
   - Mapper sentinels d'état (refus de purge prématurée → 409 `purge_not_eligible`).
   - **Garder DELETE legacy** mappé sur soft-delete pour rétro-compat, mais dépréciser dans le code.

4. **Cron `internal/migrations/cron_*` ou separate job** :
   - Quotidien : scan `purge_eligible_at < NOW() - 30d` et émettre `tenant.purge_due` au Hub (pas de purge auto, c'est Robert/Hub qui déclenche).

5. **Tests** colocalisés (Constitution §1) : 1 test par endpoint + tests machine à états (refus transitions invalides).

## Risque migration

P1 — colonnes nullables, ajout pur (Expand & Contract clean). Pas de DROP.

## Lien Hub

Ticket Hub à créer après pour vérifier que le Hub appelle bien les nouveaux endpoints (sinon le boulot est inutile). Le Hub a déjà la table `tenants` côté DB Hub avec un état mais peut-être pas la même machine à états — coordonner.
