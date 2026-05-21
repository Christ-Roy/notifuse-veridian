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

## 1. État Notifuse au 2026-05-21 (audit agent Prospection)

**Score conformité** : 13/22 endpoints livrés (59 %, parmi les meilleurs cross-app).

✅ Endpoints socle + lifecycle complets (provision, update-plan, attach-owner,
suspend/resume, health, soft-delete, restore, purge, touch, usage-summary,
magicLink, limits, auto-login, admin/grant-unlimited, admin/wipe-test-tenants,
admin/cache/invalidate).

❌ Multi-membre (§5.18-5.21) : 0/5 endpoints livrés. Ticket existant
`2026-05-19-v13-multi-membre-cross-app.md`.

❌ §5.22 attach-member (P1) : ticket existant `2026-05-21-hub-attach-member-endpoint.md`.

❌ Rotate api-key + transfer-owner (§5.15-5.16) : ticket
`2026-05-19-rotate-transfer-owner-endpoints.md`.

❌ Discovery `/users/by-email` (§5.12) : ticket
`2026-05-20-add-discovery-endpoint-by-email.md`.

## 2. Actions Notifuse immédiates pour v1.4 (P1)

### 2.1 Endpoint §5.22 attach-member (P1 bloqueur Hub)

Suivre `2026-05-21-hub-attach-member-endpoint.md`. Spec complète dans
`CONTRAT-HUB-API-REF.md` section ATTACH (route, body, response 201/200,
codes erreur, tests obligatoires).

**Spécificités Notifuse** :

- 1 workspace = 1 tenant. Donc `workspaceId` reçu = `workspace.id` Notifuse
  (= slug). Si le Hub envoie un `workspaceId` qui ne matche pas
  `workspace.id`, retourner 404.
- Ajouter au `user_workspaces(workspace_id, user_id)` avec `role` par défaut.
- Ne JAMAIS écraser un `role` existant (cf §5.22.4 contrat).

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
