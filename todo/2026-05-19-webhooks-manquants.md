# 2026-05-19 — Webhooks Notifuse → Hub manquants

> **Spec** : `../CONTRAT-HUB.md` §7.1
> **Sévérité** : 🟡 P1 fonctionnel
> **Effort** : M (2-8h)

## Contexte

Notifuse émet aujourd'hui : `tenant.provisioned`, `tenant.plan_changed`, `tenant.suspended`, `tenant.resumed`, `tenant.deleted`, `tenant.owner_changed`.

**Manquants** (§7.1 v1.2/v1.3) :

| Événement | Quand | Payload |
|---|---|---|
| `tenant.soft_deleted` | post `SoftDelete` (renomme `tenant.deleted` ou émet les deux pour compat) | `{tenant_id, reason, deleted_at, purge_eligible_at}` |
| `tenant.restored` | post `Restore` | `{tenant_id, restored_at, restored_by}` |
| `tenant.purged` | post `Purge` (hard delete) | `{tenant_id, purged_at, reason}` |
| `tenant.touched` | post `Touch` (debounced 24h) | `{tenant_id, touched_at, soft_delete_eligible_at}` |
| `tenant.quota_exceeded` | premier crossing au-dessus du quota dans le mois | `{tenant_id, plan, quota, sent, exceeded_at}` |
| `tenant.member_added` | si v1.3 cross-app actif | `{tenant_id, user_email, role, source}` |
| `tenant.member_removed` | idem | `{tenant_id, user_email, source}` |
| `tenant.member_role_changed` | idem | `{tenant_id, user_email, old_role, new_role}` |

## Travail

1. **`internal/domain/veridian.go`** : ajouter les `VeridianEvent` constantes.
2. **`internal/service/veridian_service.go`** : wire les emits dans les fonctions correspondantes (SoftDelete/Restore/Purge/Touch viennent du ticket lifecycle).
3. **`tenant.quota_exceeded`** : add un check dans la queue email worker (probablement `internal/service/email_service.go` ou worker) qui détecte le franchissement et émet une fois par mois max (`emitted_at` dans `veridian_plan` ou table dédiée).
4. **Tests** sur `veridian_service_test.go`.

## Dépendances

- `tenant.soft_deleted`, `tenant.restored`, `tenant.purged`, `tenant.touched` → dépendent du ticket `lifecycle-soft-delete-restore-purge`.
- `tenant.member_*` → dépendent du ticket `multi-membre-cross-app` (v1.3).
- `tenant.quota_exceeded` → standalone, peut être shippé tout seul.

## Reco shipping

Ship `tenant.quota_exceeded` en standalone (~2h, utile immédiatement pour le Hub qui peut envoyer un email "tu as atteint ton quota"). Le reste attend les chantiers parents.
