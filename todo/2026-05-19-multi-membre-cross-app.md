# 2026-05-19 — Multi-membre cross-app (v1.3)

> **Spec** : `../CONTRAT-HUB.md` §5.18 (sync) + §5.19 (remove) + §5.20 (restore) + §5.21 (seat overage)
> **Sévérité** : 🟢 P3 (feature v1.3 non urgente — Hub décide quand activer)
> **Effort** : L (1-2 jours)

## Contexte

Contrat v1.3 (2026-05-19) introduit le **multi-membre cross-app** : un workspace tenant peut avoir N humains attachés (Option C), avec sync depuis le Hub. Aujourd'hui Notifuse a juste 1 owner humain par workspace (post fix idempotence b7d3fdcc).

**Quand l'activer** : quand un client multi-team paye un plan multi-seat sur le Hub et que ce client doit voir ses N team members listés dans Notifuse.

## Endpoints à créer

| Méthode | Path | Body | Réponse |
|---|---|---|---|
| `POST` | `/api/tenants/{id}/sync-member` | `{user_email, role, source}` | `{tenant_id, user_email, action: "added|updated|already"}` |
| `POST` | `/api/tenants/{id}/remove-member` | `{user_email, reason}` | `{tenant_id, user_email, removed_at}` |
| `POST` | `/api/tenants/{id}/restore-member` | `{user_email}` | `{tenant_id, user_email, restored_at}` |

## Travail

1. **`service.SyncMember(ctx, input)`** : upsert dans `user_workspaces` avec role mapping (`owner|admin|member`). Idempotent.

2. **`service.RemoveMember(ctx, input)`** : soft-delete (set `removed_at` sur `user_workspaces`, pas DELETE) pour pouvoir restaurer.

3. **`service.RestoreMember(ctx, input)`** : clear `removed_at`.

4. **Soft warning seat overage** (§5.21) : si tenant dépasse `max_seats` de son plan, marquer le tenant `overage_warn_at = NOW()`. Si toujours en overage après 7j, émettre `tenant.seat_overage_persistent` et le Hub peut suspendre.

5. **Webhooks** `tenant.member_added`, `tenant.member_removed`, `tenant.member_role_changed` (cf ticket webhooks).

## Risque

P3 — feature additive complète. Migration : colonne `removed_at` nullable sur `user_workspaces`.

## Reco

**Ne PAS faire avant** que le Hub ait des plans multi-seat actifs. Sinon c'est du code mort. Ticket reste en attente.
