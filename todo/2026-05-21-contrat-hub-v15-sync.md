# [NOTIFUSE] Sync v1.5 contrat — alignement docs + actions Notifuse

> **Type** : Mise à jour contractuelle + alignement Notifuse
> **Sévérité** : 🟡 P2 (doc) + 🔴 P1 sur quelques actions (cf §2)
> **Owner** : agent Notifuse
> **Créé** : 2026-05-21 par l'agent Prospection (suite brainstorm Robert)
> **Réfère** : `veridian-hub/docs/CONTRAT-HUB.md` v1.5 + `veridian-hub/docs/CONTRAT-HUB-API-REF.md` v1.1

## Contexte

Suite au brainstorm Robert 2026-05-21 sur la sync workspace cross-app, bump
du contrat v1.3 → v1.4 → v1.5 :

**v1.4 (matin)** :

- **§1.4 Hub source de vérité + résilience apps** : règle fondamentale.
  Apps doivent survivre Hub-down. Anti-pattern : call Hub synchrone hot path.
- **§3.7 Modèle d'identité user cross-app** : email canonique + colonne
  nullable `users.hub_user_id` côté apps. Pas de migration destructive.
- **§4.4 Cycle de vie d'un membre** : 2 manières exclusives (owner OU
  invité). Signup Hub ne crée AUCUN tenant ni membership.
- **§5.22 Invitation cross-app workspace-level (P1)** : endpoint
  `attach-member` workspace-level. Ticket
  `2026-05-21-hub-attach-member-endpoint.md` couvre cette livraison.
- Nouveau doc compagnon **CONTRAT-HUB-API-REF.md**.

**v1.5 (soir)** :

- **§5.18.2 [DÉPRÉCIÉ]** : doublon admin invite-member supprimé. Tout
  passe par P1 §5.22.
- **§5.22.5 réécrit** : tableau 3 endpoints membre actifs + règles de choix.
- **§6bis.7 nouveau** : logout cross-app (modèle "logout local") + scope
  cookie obligatoire `.staging.veridian.site` en staging.
- **§7.1 webhooks** : ajoute `tenant.member_role_changed`, `member_added`,
  `member_removed`.
- **§11bis nouveau** : Permissions et droits utilisateur cross-app. Matrice
  des droits par action + pattern de vérification d'auth + cas particuliers.
- API-REF v1.1 : section **PERMS** avec helpers + matrice endpoint → droit.

## 1. État Notifuse au 2026-05-21 (audit agent Prospection) — MAJ 2026-05-21 soir

**Score conformité** : ~15/22 endpoints livrés (~68 %, en tête cross-app post-sprint).

✅ Endpoints socle + lifecycle complets (provision, update-plan, attach-owner,
suspend/resume, health, soft-delete, restore, purge, touch, usage-summary,
magicLink, limits, auto-login, admin/grant-unlimited, admin/wipe-test-tenants,
admin/cache/invalidate, admin/tenants listing).

✅ **§5.22 attach-member (P1)** : LIVRÉ sprint 2026-05-21 lot B (commit `7f3adccb`).
Voir `todo/done/2026-05-21-hub-attach-member-endpoint.md`. Tests 13 handler + 12 service.

✅ **§5.12 Discovery `/users/by-email`** : LIVRÉ sprint 2026-05-21 lot D
(commits `7f3adccb`/`9534f0d4`/`b694609f`). Voir
`todo/done/2026-05-20-add-discovery-endpoint-by-email.md`.

❌ Multi-membre (§5.18-5.21) : 0/5 endpoints livrés. Ticket existant
`2026-05-19-v13-multi-membre-cross-app.md` (pas bloquant aujourd'hui).

❌ Rotate api-key + transfer-owner (§5.15-5.16) : ticket
`2026-05-19-rotate-transfer-owner-endpoints.md` — Hub a un fallback,
P3 cosmétique d'après l'audit 2026-05-19.

❌ Webhooks lifecycle/quota manquants : `2026-05-19-webhooks-manquants.md`.

## 2. Actions Notifuse immédiates pour v1.4 (P1)

### 2.1 ✅ Endpoint §5.22 attach-member — LIVRÉ sprint 2026-05-21 lot B

Commit `7f3adccb`. Spec respectée :
- HMAC Hub via middleware existant (réutilise `HUB_API_SECRET`)
- Body `{hub_user_id, hub_user_email, role, invitation_id}`
- Response 201 first attach, 200 already_member idempotent
- Codes 401/404/400/423/409 standard
- Lookup user par `hub_user_id`, INSERT/UPDATE `user_workspaces`
- Génère `login_url` magic link auto-login
- Tests : 13 handler + 12 service (Constitution §1 OK)

Hub peut désormais débloquer phase 4b invitation (`accept.ts` retourne 200 + login_url).

### 2.2 Colonne `users.hub_user_id` (§3.7)

Migration additive :

```sql
ALTER TABLE users ADD COLUMN hub_user_id UUID NULL;
CREATE UNIQUE INDEX users_hub_user_id_uniq ON users(hub_user_id) WHERE hub_user_id IS NOT NULL;
```

Backfill au premier contact via HMAC :

```sql
UPDATE users SET hub_user_id = $1
WHERE id = (SELECT id FROM users WHERE email = $2 LIMIT 1)
  AND hub_user_id IS NULL;
```

À déclencher dans provision, attach-member, sync-member, update-plan.

### 2.3 Vérifier le secret HMAC staging

Selon §6.5 du contrat v1.1, dette restante : `NOTIFUSE_HUB_API_SECRET_STAGING`
n'est PAS dans les GitHub Secrets du repo `veridian-hub`. À fixer côté Hub
(ticket `2026-05-21-contrat-hub-v15-sync.md` §2.1 côté Hub).

Côté Notifuse, vérifier que le `.env.staging` contient bien la VALEUR du
secret (pas le fallback compose `staging-secret`).

## 3. Actions de fond v1.4 (P2)

### 3.1 Endpoints §5.18-5.21 multi-membre

Ticket existant `2026-05-19-v13-multi-membre-cross-app.md` — re-prioriser
maintenant que :
- L'agent Hub livre P1 invitation (5/9 étapes faites)
- Le contrat v1.4 grave définitivement la sémantique (§5.18 admin push vs
  §5.22 self-service tenant)

À livrer (specs dans CONTRAT-HUB-API-REF.md) :
- `POST /api/tenants/{id}/sync-member` (§5.18.3)
- `POST /api/tenants/{id}/remove-member` (§5.19.2)
- `POST /api/tenants/{id}/restore-member` (§5.20)
- `POST /api/tenants/{id}/freeze-members` + `unfreeze-members` (§5.21)
- Webhook `tenant.member_role_changed` app→Hub (§5.18.4)

### 3.2 Endpoint `/users/by-email` (§5.12 discovery)

Ticket existant `2026-05-20-add-discovery-endpoint-by-email.md` — spec
finalisée dans `CONTRAT-HUB-API-REF.md` section EMAIL.

### 3.3 Webhooks manquants

Ticket existant `2026-05-19-webhooks-manquants.md`. Compléter pour
émettre `tenant.touched`, `tenant.owner_changed`, `tenant.quota_exceeded`,
`tenant.member_role_changed`.

## 4. Référence

- `veridian-hub/docs/CONTRAT-HUB.md` v1.4 (3443L)
- `veridian-hub/docs/CONTRAT-HUB-API-REF.md` v1.0 (1277L)
- Audit agent Prospection 2026-05-21 (3 rapports cross-app)

## Réponse attendue

Sous `## Réponse — YYYY-MM-DD` en fin de ce fichier, puis `done/` une fois
toutes les actions §2 traitées (§3 reste P2, peut prendre plusieurs jours).
