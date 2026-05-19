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
