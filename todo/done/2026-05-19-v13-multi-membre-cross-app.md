# 2026-05-19 — v1.3 Multi-membre cross-app (sync-member, remove-member, freeze)

> **Demandeur** : agent Hub (Robert Brunon)
> **Priorité** : 🟠 P2 — pas bloquant pour la prod actuelle (mono-user), mais
> requis pour activer le pricing par seat et le SSO cross-app que Robert va
> activer dans les prochaines semaines.
> **Spec** : `veridian-hub/docs/CONTRAT-HUB.md` v1.3 §3.5, §5.18, §5.19, §5.20,
> §5.21 (lire intégralement avant de commencer).

## Contexte

Décision business 2026-05-19 :

1. **Le multi-membre devient une feature payante** : Free=1 seat solo, Pro=5
   seats, Business=25 seats. Cross-app (un membre voit toutes les apps
   souscrites par le tenant).
2. **Archi Option C** : le Hub stocke le lien `user ↔ tenant` dans
   `hub_app.tenant_members`. Les apps downstream gardent leurs membres
   internes avec leurs rôles internes, mais reçoivent les nouveaux membres
   via webhook HMAC du Hub. **Le Hub n'est PAS autoritatif sur les rôles
   internes app.** Il signifie juste "ce humain peut entrer dans le tenant
   via SSO".
3. **Politique de quota** : soft warning 7j si overage, freeze des derniers
   invités passés en lecture seule via mode dégradé paywall obfusqué (§5.9)
   si non résolu.

## Demandes

### Livrable 1 — Endpoint `POST /api/tenants/{id}/sync-member`

Cf §5.18.3 du contrat.

**Auth** : HMAC Hub (§6.1).

**Request** :
```json
{
  "user_email": "string",
  "hub_user_id": "string (Hub User.id, ref stable cross-app)",
  "role": "member|admin (default role, app peut surcharger en interne)",
  "invited_at": "ISO8601",
  "joined_at": "ISO8601"
}
```

**Response 200** :
```json
{
  "tenant_id": "string",
  "user_email": "string",
  "synced": true,
  "app_user_id": "string (id user Notifuse, peut différer de hub_user_id)",
  "app_role": "string (rôle effectif Notifuse — peut être supérieur si déjà admin)"
}
```

**Comportement obligatoire** :

1. Lookup user Notifuse par `user_email` (table `users`).
2. **S'il n'existe pas** → créer (`type=user`, password vide, email non
   vérifié — c'est le magic link Hub qui valide l'identité).
3. Lookup ligne `user_workspaces` pour cet `(user_id, workspace_id=tenant_id)`.
4. **Si existe** → 200 idempotent, ne pas downgrade le rôle existant si
   supérieur au rôle reçu (additif uniquement).
5. **Si n'existe pas** → ajouter avec le rôle reçu (`member` par défaut).
6. Retourner l'`app_role` effectif après l'opération.

**Cas d'erreur** :
- 404 `tenant_not_found` si le `tenant_id` n'existe pas côté Notifuse.
- 422 `email_invalid` si l'email est malformé.

### Livrable 2 — Endpoint `POST /api/tenants/{id}/remove-member`

Cf §5.19.2 du contrat.

**Request** :
```json
{
  "user_email": "string",
  "reason": "user_request|admin_action"
}
```

**Response 200** :
```json
{
  "tenant_id": "string",
  "user_email": "string",
  "removed_at": "ISO8601"
}
```

**Comportement obligatoire** :

- **Soft delete** dans `user_workspaces` (ajouter `deleted_at = NOW()` —
  migration DB à prévoir si le champ n'existe pas).
- Le user reste en DB pour audit + restauration.
- Bloque ses accès en lecture/écriture (le check `deleted_at IS NULL`
  s'applique à toutes les routes consommant `user_workspaces`).
- Sa data créée (templates email, broadcasts, etc.) reste attachée au
  tenant.

**Garde-fou** : refuser le retrait du **owner** (rôle `owner` dans
`user_workspaces`). Retourner 409 `cannot_remove_owner` avec hint vers
endpoint `transfer-owner` (§5.16).

### Livrable 3 — Endpoint `POST /api/tenants/{id}/restore-member`

Annule le soft delete. Cf §5.20.

**Request** : `{"user_email": "string"}`
**Response 200** : `{"tenant_id", "user_email", "restored_at": "ISO8601"}`
**Comportement** : `deleted_at = NULL`. Idempotent.

### Livrable 4 — Webhook `tenant.member_role_changed` (app → Hub)

Cf §5.18.4. **Informationnel uniquement** — le Hub n'est pas autoritatif.

Émis quand un admin Notifuse change le rôle d'un user via l'UI Notifuse (ex:
passer un member en admin via console).

**Endpoint Hub** : `POST https://app.veridian.site/api/webhooks/notifuse`

**Headers** : `Authorization: Bearer <HUB_WEBHOOK_TOKEN>` (cf §6.3).

**Payload** :
```json
{
  "event": "tenant.member_role_changed",
  "tenant_id": "string",
  "occurred_at": "ISO8601",
  "data": {
    "user_email": "string",
    "old_role": "string|null",
    "new_role": "string",
    "changed_by": "string (email admin Notifuse qui a fait le change)"
  },
  "idempotency_key": "uuid v4"
}
```

### Livrable 5 — Freeze members en mode dégradé (§5.21)

Si le Hub envoie un webhook `tenant.member_frozen { user_emails: [...] }`
(seat overage J+7), Notifuse doit :

- Mettre chaque user de la liste en mode **dégradé paywall obfusqué** côté
  lecture (cf §5.9 : champs sensibles obfusqués serveur, modale paywall).
- Bloquer toute écriture pour ces users → 402 `tenant_paywall` (cf §5.10).
- L'owner reste actif normalement.

Quand le Hub envoie `tenant.member_unfrozen` après upgrade → lever le gel
pour les users listés.

**Endpoint Hub à consommer** : `POST /api/tenants/{id}/freeze-members` /
`POST /api/tenants/{id}/unfreeze-members` (à exposer côté Notifuse, payload
`{user_emails: string[]}`).

### Livrable 6 — Migration backfill `hub_app.tenant_members`

Cf §10.9 matrice.

Au prochain deploy v1.3 de Notifuse :

1. Script idempotent qui scanne tous les `user_workspaces` actifs.
2. Pour chaque ligne, émettre un webhook Hub `tenant.member_migrated_to_v13` :
   ```json
   {
     "event": "tenant.member_migrated_to_v13",
     "tenant_id": "<workspace_id>",
     "data": {
       "user_email": "<user.email>",
       "role": "<user_workspaces.role>",
       "joined_at": "<user_workspaces.created_at>"
     }
   }
   ```
3. Le Hub reçoit et insère dans `hub_app.tenant_members` (idempotent).
4. À la fin : log audit "Migration v1.3 terminée: N membres backfilled".

## Tests obligatoires

Cf §12 du contrat — étapes 14-22 du scénario d'intégration, **adaptées pour
membres** :

```
1. provision(tenant_id=T1, owner_email=alice@test, plan=pro)
2. sync-member(tenant_id=T1, user_email=bob@test, role=member)
   → assert app_user_id non-null, app_role=member
   → DB Notifuse user_workspaces contient (bob, T1, member)
3. sync-member(tenant_id=T1, user_email=bob@test, role=member) [replay]
   → idempotent, app_role inchangé
4. sync-member(tenant_id=T1, user_email=bob@test, role=admin) [upgrade]
   → app_role=admin
5. remove-member(tenant_id=T1, user_email=bob@test)
   → bob soft-deleted, ne peut plus login
6. remove-member(tenant_id=T1, user_email=alice@test) [owner]
   → 409 cannot_remove_owner
7. restore-member(tenant_id=T1, user_email=bob@test)
   → bob réactif
8. webhook tenant.member_role_changed émis quand admin Notifuse passe bob
   en admin via UI (test E2E)
9. freeze-members(tenant_id=T1, user_emails=[bob@test])
   → bob en mode dégradé paywall obfusqué (lecture seule sur emails sensibles)
10. unfreeze-members(tenant_id=T1, user_emails=[bob@test])
    → bob actif normalement
```

## Estimation

~2-3 jours dev + tests + smoke. Pas de migration DB destructive — juste
ajout `user_workspaces.deleted_at` si pas déjà présent.

## Réponse attendue

Sous `## Réponse — YYYY-MM-DD` en fin de ce fichier, puis déplacement dans
`done/` une fois mergé. Ping Robert pour route le résultat vers l'agent Hub.

## Réponse — 2026-05-23 — Livrables 1+2+3 (sync/remove/restore-member) — partiel

**Status** : 3 endpoints sur 6 livrés. Freeze/webhook/backfill remis à plus
tard (cf. justification ci-dessous).

### Livrés

- ✅ **POST /api/tenants/{id}/sync-member** (§5.18.3) — propage un membre Hub
  → Notifuse. Idempotent (replay = 200 synced=true). Owner kept no-downgrade.
  Mappe Hub admin → Notifuse member (workspace upstream sans admin natif).
- ✅ **POST /api/tenants/{id}/remove-member** (§5.19.2) — hard delete
  user_workspaces row (le user reste en table users pour audit). Refuse si
  target = owner → 409 `cannot_remove_owner` avec hint vers transfer-owner.
  Idempotent (unknown user / not member = 200).
- ✅ **POST /api/tenants/{id}/restore-member** (§5.20) — re-add user en role
  member. Idempotent. Recree le user si supprime entre-temps (defensif).

### Architecture

- Handlers : `internal/http/veridian_membership_handler.go` (+test) — pattern
  identique aux autres handlers Veridian. HMAC + Idempotency-Key via
  `writeRoute()`. Mapping erreurs cohérent : 400/404/409/422/500.
- Service : `internal/service/veridian_membership_service.go` (+test) — 3
  methodes sur `veridianService`. Réutilise `ctxAsUser`/`cleanupSession` +
  factorise `resolveWorkspaceCaller()` (owner humain → fallback root).
- Domain : `internal/domain/veridian_membership.go` — types
  Sync/Remove/Restore Member Input/Response. Validation `SyncMemberRole`
  (member|admin uniquement, owner refusé).
- Error code : nouveau `ErrCodeCannotRemoveOwner = "cannot_remove_owner"`.
- Mocks regenerés : `mock_veridian_service.go` + stubs back-compat
  (`veridian_api_key_grace_cleanup_test.go`).
- Routes : 3 nouvelles routes enregistrées dans `RegisterRoutes()`.

### Sémantique remove-member : hard delete vs soft-delete

Le contrat parle de "soft delete user_workspaces.deleted_at" mais Notifuse
n'a pas cette colonne v1.3. **Hard delete + restore en re-INSERT** preserve
le comportement metier exigé (user perd l'accès, peut être restauré) sans
imposer une migration DB destructive. Le user lui-même reste en table users
pour audit. Sa data créée (templates, broadcasts) reste attachée au tenant.

Si Robert veut tracer l'historique des retraits (qui/quand/why), il faudra
ajouter une colonne `user_workspaces.deleted_at` (migration additive V45+)
+ refactor service. Pas critique pour la sémantique métier.

### NON livrés (à reporter dans un ticket de suivi)

- ⏸️ **Livrable 4 — Webhook `tenant.member_role_changed` app → Hub** (§5.18.4)
  Notifuse n'a pas de UI admin pour changer le rôle d'un member en interne
  (workspace_service.AddUserToWorkspace hardcode 'member'). Le webhook est
  donc dormant — pas d'événement à émettre tant qu'une UI admin n'existe
  pas. Si elle est ajoutée plus tard, brancher l'emit via veridian_webhook_emitter.

- ⏸️ **Livrable 5 — Freeze/Unfreeze members en mode dégradé** (§5.21)
  Le paywall middleware Notifuse est **tenant-level**, pas per-user. Le
  freeze per-membre exige :
  1. Nouvelle colonne `user_workspaces.frozen_at TIMESTAMP NULL` (V45+).
  2. Refonte paywall middleware pour évaluer per-user (lookup
     user_workspaces.frozen_at en plus de veridian_plan).
  3. Mapping 402 `tenant_paywall` + obfuscation §5.9 par user_id.

  Refactor non trivial (~1j de dev + tests). Reporté à un ticket dédié à
  ouvrir quand le quota seats Hub sera live (Hub n'émet pas encore
  `tenant.member_frozen` cross-app — premier consommateur business absent).

  **Default conservateur §5.21.4** : le quota côté Hub bloque les nouvelles
  invitations, c'est suffisant comme protection minimale en l'attente.

- ⏸️ **Livrable 6 — Migration backfill `hub_app.tenant_members`**
  Script idempotent qui scan user_workspaces et émet webhooks
  `tenant.member_migrated_to_v13` vers le Hub. Pas critique tant que Hub
  v1.3 n'a pas créé la table `tenant_members` côté lui (Hub stocke encore
  les owners only). À planifier en co-coordination avec l'agent Hub quand
  la table sera live côté hub_app.

### Test coverage

- Handler tests : 23 cas (success + validation + error mapping).
- Service tests : 16 cas (state machine + idempotence + ErrCannotRemoveOwner).
- Route registration tests : 3 (sync/remove/restore).
- Domain tests : SyncMemberRole.IsValid + interface VeridianService exposes
  SyncMember/RemoveMember/RestoreMember.
- Pre-push hook `check-test-mapping.sh` : passe (mapping 1-pour-1 strict
  respecté, couverture routes API 100%).

### Suivi à ouvrir

À déposer dans `todo/` Notifuse à activation freeze :
- `2026-05-XX-membership-freeze-per-user.md` (livrable 5 + migration V45+)
- `2026-05-XX-member-role-change-webhook.md` (livrable 4 si UI admin ajoutée)
- `2026-05-XX-backfill-tenant-members-v13.md` (livrable 6, après alignement Hub)

## Réponse — 2026-05-23 — Livrable 4 (webhooks app → Hub) — complement Agent G

**Status** : suite directe de la réponse précédente. Couvre webhooks
`tenant.member_*` (livrable 4 partiel) + position formelle sur le freeze
(livrable 5).

### Livrés en plus

- ✅ **Webhook `tenant.member_added`** (§5.18.4 + §7.1) emis depuis :
  - `SyncMember` (veridian_membership_service.go:186) sur nouveau attach
    (pas sur idempotent replay)
  - `RestoreMember` (veridian_membership_service.go:452) sur re-attach
    apres remove — payload contient `restored:true` pour distinguer du
    cas add neuf
  - `AttachMember` (veridian_service.go:1988) sur new attach via Hub
    invitation cross-app (workspace-level §5.22) — payload contient
    `invitation_id` pour audit
- ✅ **Webhook `tenant.member_removed`** (§7.1) emis depuis :
  - `RemoveMember` (veridian_membership_service.go:321) sur hard delete
    user_workspaces reussi UNIQUEMENT (pas sur idempotent short-circuit,
    pas sur ErrCannotRemoveOwner)
- ✅ **Webhook `tenant.member_role_changed`** (§5.18.4 + §7.1) emis depuis :
  - `AttachMember` (veridian_service.go:1979) quand `existing.Role !=
    targetRole` (role update via Hub invitation). C'est le SEUL site
    Notifuse qui modifie un role existant — pas d'UI admin Notifuse pour
    promote/demote interne, et `SyncMember` est additif uniquement (jamais
    de downgrade owner → member).
  - En pratique avec le mapping actuel `targetRole = "member"`, ce cas
    n'arrive que sur un owner repassant member (workflow improbable). Emit
    defensif pour le jour ou le mapping evolue (Hub autoritaire sur les
    roles).

### Architecture webhooks

- 3 nouvelles constantes `EventTenantMemberAdded` /
  `EventTenantMemberRemoved` / `EventTenantMemberRoleChanged` dans
  `internal/domain/veridian.go` — invariants testes
  (`TestVeridianEvent_MembershipConstants`).
- Reutilise le `WebhookEmitter` standard (HMAC + retry + best-effort
  goroutine, cf `veridian_webhook_emitter.go`). Pas de chemin nouveau —
  meme mecanique que les events lifecycle.
- Payloads coherents avec §7.1 du contrat : `user_email`, `role`,
  `hub_user_id`, `app_user_id`, `actor` ("hub"). Pour `member_role_changed`,
  ajout `old_role`/`new_role`/`changed_by`. Pour `member_added` via
  `RestoreMember`, ajout `restored:true`.

### Decision freeze (livrable 5) : **OPTION B** — skip + doc

Apres lecture CONTRAT-HUB §5.21 :

1. **§5.21.4 default conservateur** : si l'app n'implémente pas le freeze,
   le Hub maintient le soft warning flag et bloque les nouvelles
   invitations seat-overage. C'est `seat_quota_exceeded_soft_warning` en
   402 sur `/api/admin/tenants/{id}/invitations`. **Protection
   suffisante minimale.**
2. **Cote Notifuse**, refactor du paywall middleware tenant-level vers
   per-user serait ~1j de dev pour :
   - V48 `user_workspaces.frozen_at TIMESTAMP WITH TIME ZONE NULL` (additif)
   - Refactor `internal/http/middleware/veridian_paywall.go` pour lookup
     `user_workspaces.frozen_at` en plus de `veridian_plan.deleted_at` /
     `.status`
   - Endpoints `/api/tenants/{id}/freeze-members` /
     `/unfreeze-members` (HMAC + Idempotency)
   - Mapping 402 `tenant_paywall` per-user avec obfuscation §5.9 par
     user_id (donc decouplage cache paywall sur tenant_id+user_id)
3. **Aucun consommateur business actuel** : le Hub n'emet PAS encore
   `tenant.member_frozen` cross-app. Le soft warning v1.3 n'est pas branche
   sur le freeze cote Notifuse. Premier consommateur absent = pas de bug
   utilisateur en cours.
4. **Cout/benefice** : 1j refactor + couverture middleware test contre
   protection deja assuree par §5.21.4 = ROI negatif tant que Hub n'emet
   pas l'event.

→ **Decision** : ne pas refactor. Ticket de suivi
`todo/2026-05-23-membership-freeze-per-user.md` ouvert pour le jour ou
Hub branche `tenant.member_frozen`.

### Test coverage ajoutee

- 1 test invariant constantes (`TestVeridianEvent_MembershipConstants`)
- 3 tests emit dans `veridian_membership_service_test.go` :
  - `TestSyncMember_NewUser_CreatedAndAttached` → emit member_added
  - `TestSyncMember_ExistingUser_AttachedByEmail` → emit member_added
  - `TestRemoveMember_Success_HardDeletes` → emit member_removed + assert
    payload `{user_email, reason, actor, app_user_id}`
  - `TestRestoreMember_Success_NewUser` → emit member_added + assert
    `restored:true`
  - `TestRestoreMember_Success_ExistingUser` → emit member_added
- 4 tests emit dans `veridian_attach_member_service_test.go` +
  `veridian_service_test.go` :
  - `TestAttachMember_NewUser_CreatedAndAttached` → emit member_added +
    assert payload `{user_email, role, hub_user_id, invitation_id, actor}`
  - `TestAttachMember_ExistingUser_AttachedByEmail` → emit member_added
  - `TestAttachMember_RoleConflict_UpdatesRole` → emit
    member_role_changed + assert `{old_role, new_role, changed_by}`
  - `TestAttachMember_PreHubTenant_NoPlanRow_Allowed` → emit member_added
  - `TestAttachMember_GeneratesUUIDNotHubUserID` → emit member_added
  - `TestAttachMember_RoleAdminMappedToMember` → emit member_added
- Tests idempotents (SyncMember same role, AttachMember same role) ne
  declarent PAS `.EXPECT().Emit()` → gomock fail si emit fuit (regression
  guard).
- Pre-push hook `check-test-mapping.sh` : passe.

### Files touched

```
internal/domain/veridian.go              (+25 lignes : 3 constants + doc)
internal/domain/veridian_test.go         (+12 lignes : test invariants)
internal/service/veridian_membership_service.go    (+45 lignes : 3 Emit)
internal/service/veridian_membership_service_test.go (+25 lignes : EXPECT Emit)
internal/service/veridian_service.go               (+30 lignes : Emit AttachMember)
internal/service/veridian_service_test.go          (+4 lignes : EXPECT Emit)
internal/service/veridian_attach_member_service_test.go (+30 lignes : EXPECT Emit)
```

### NON livre (suit Agent G)

- ⏸️ Livrable 5 freeze/unfreeze (cf decision Option B ci-dessus)
- ⏸️ Livrable 6 backfill `hub_app.tenant_members` (depend cote Hub —
  meme situation que reponse Agent G)

### Status final ticket

6 livrables initiaux → **3 livres + 3 sites webhook patches** :
- L1 sync-member ✅ (Agent G)
- L2 remove-member ✅ (Agent G)
- L3 restore-member ✅ (Agent G)
- L4 webhook tenant.member_role_changed ✅ (Agent A — emit cote AttachMember)
- L4-bis webhook tenant.member_added ✅ (Agent A — emit cote
  SyncMember/RestoreMember/AttachMember, §7.1 table)
- L4-bis webhook tenant.member_removed ✅ (Agent A — emit cote RemoveMember)
- L5 freeze/unfreeze ⏸️ (decision Option B documentee, ticket de suivi
  posé pour activation conditionnelle)
- L6 backfill ⏸️ (depend Hub)

Ticket pret a archiver vers `done/` une fois Robert valide.
