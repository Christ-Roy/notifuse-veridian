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

## Réponse — 2026-05-19 (claude-veridian-notifuse)

**Bloqué : `IncrementEmailsSent` n'est appelé NULLE PART dans le code Notifuse aujourd'hui.**

Recherche complète :
- `internal/repository/veridian_plan_postgres.go:201` : la méthode existe
- `internal/domain/veridian.go:148` : l'interface l'expose
- **Aucun callsite dans `internal/service/`, `internal/http/`, ou ailleurs**

Conséquences :
1. `veridian_plan.emails_sent_this_month` reste à 0 pour tous les tenants
2. `paywall middleware IsBlocked()` ne bloque jamais sur le quota (compteur jamais incrémenté)
3. Émettre `tenant.quota_exceeded` sans crossing détectable = code mort

**Pré-requis avant ce ticket** : wirer `planRepo.IncrementEmailsSent(workspaceID, 1)` dans le pipeline d'envoi des emails (probablement `internal/service/broadcast_*.go` ou le worker SES/SMTP qui dispatch). Effort ~4h supplémentaires.

**Reco** : créer un ticket dédié `wire-increment-quota.md` AVANT d'attaquer celui-ci, ou regrouper les deux. Sinon on ship un webhook qui ne s'émet jamais.

Les autres webhooks listés (`tenant.soft_deleted`, `tenant.restored`, etc.) dépendent du ticket `lifecycle-soft-delete-restore-purge` qui n'est pas implémenté non plus.

**Action pour Robert** : faut-il (a) prioriser le wiring increment + ship `tenant.quota_exceeded` ensemble, ou (b) attendre les autres chantiers et ship en bloc, ou (c) skipper le quota webhook (le Hub peut polling `/api/tenants/{id}/status` à la place — moins efficace mais zéro nouveau code Notifuse) ?

### Investigation suite — pipeline d'envoi

Reconnaissance complète :

- `internal/service/email_service.go:160` `EmailService.SendEmail` est le point central qui dispatch vers les providers (SMTP/SES/Mailgun/...). C'est un fichier **upstream-pur** (pas de préfixe `veridian_`), donc l'augmenter directement viole la convention §veridian-override (CLAUDE.md).
- `messageHistoryRepo.Create` est appelé 4 fois (= 1 par email envoyé) :
  - `transactional_service.go:874`
  - `demo_service.go:1745`
  - `broadcast_service.go:1079`
  - `broadcast/message_sender.go:650` (worker async — envois mass)
- Architecture async : les broadcasts envoient en background, l'increment ne peut donc pas se faire au moment du HTTP response — il faut le faire dans le worker.

**Options d'implémentation propres** :

1. **Décorateur sur `domain.MessageHistoryRepository.Create`** : wrapper Veridian dans `internal/repository/veridian_message_history_decorator.go` qui appelle l'original puis `planRepo.IncrementEmailsSent`. Pas de patch upstream, mais demande de rerouter l'injection dans `internal/app/app.go` (à wrapper avant de le passer aux services).
2. **Hook explicite dans `EmailService.SendEmail`** : ajouter un callback `OnEmailSent` que le code Veridian peut brancher. Patch upstream MINIMAL (3 lignes) mais quand même une exception.
3. **Worker dédié** : cron qui scanne `message_history` toutes les 60s et sync le compteur. Découplé mais latence et coût DB.

**Reco** : option 1 (decorator) — strictement Veridian, idiomatique Go, testable en isolation.

**Skip pour cette session** : trop gros pour être pris à la suite des 2 chantiers déjà livrés (~6h+ avec tests). À planifier comme ticket dédié.
