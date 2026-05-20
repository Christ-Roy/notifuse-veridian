# [NOTIFUSE] Endpoint attach-member pour invitations Hub

> **Type** : Endpoint contrat cross-app, demandé par le Hub
> **Sévérité** : 🔴 P1 — bloque la phase 4b du P1 invitation Hub
> **Owner** : agent Notifuse (Go)
> **Créé** : 2026-05-21 par l'agent Hub
> **Bloque** : `veridian-hub/lib/invitations/accept.ts` (TODO P1-step4b)

## Contexte

Le Hub a livré 5/9 étapes du P1 invitation endpoints (2026-05-21).
Aujourd'hui, quand un user accepte une invitation cross-app pour un
workspace Notifuse, **le Hub marque l'invitation acceptée dans sa
table mais ne fait rien côté Notifuse**. Conséquence : l'user n'a pas
accès au workspace cible côté Notifuse.

Le Hub renvoie volontairement 202 Accepted sur
`POST /api/invitations/[token]/accept` pour signaler ce trou.

Pour finir la boucle, Notifuse doit exposer un endpoint que le Hub
appelle en HMAC après acceptation.

## Spec endpoint à livrer

**Route** : `POST /api/tenants/{tenantId}/attach-member`

Cohérent avec les routes existantes Hub→Notifuse
(`/api/tenants/provision`, `/api/tenants/{id}/suspend`, etc.).

⚠️ Vocabulaire Veridian : ce que le Hub appelle `workspace_id` côté
invitation correspond ici au `tenant_id` Notifuse (slug ou UUID selon
convention). L'agent Hub envoie `target_workspace_id = tenant_id`.

### Auth — HMAC Hub

Réutiliser le middleware HMAC Notifuse existant :
`internal/http/middleware/veridian_hmac.go`.

Headers attendus :
```
X-Veridian-Timestamp: 1747857600000
X-Veridian-Hub-Signature: <hex sha256>
```

Secret env Notifuse : utiliser le **même** secret que pour les autres
routes Hub→Notifuse. Pas de nouveau secret dédié — l'agent Hub a un
secret par app downstream (`HUB_INVITATION_SECRET_NOTIFUSE`), et
Notifuse doit aligner son secret de réception Hub existant.

### Body

```json
{
  "hub_user_id": "user_abc123",
  "hub_user_email": "alice@example.com",
  "role": "member",
  "invitation_id": "inv_xyz"
}
```

- `hub_user_id` : id côté `hub_app.users` — Notifuse stocke cet id
  pour cohérence cross-app (le user Notifuse existant `user_id`
  Veridian doit déjà être ce champ).
- `hub_user_email` : pour création user Notifuse si absent (similaire
  au flow `provision` existant).
- `role` : `owner | admin | member`. Mapper aux roles internes
  Notifuse si différents (la table `user_workspaces` actuelle a
  probablement un enum).
- `invitation_id` : traçabilité audit ("attached via Hub invitation X").

### Réponse

Succès (201) :
```json
{
  "attached": true,
  "already_member": false,
  "workspace_id": "<tenantId>",
  "role": "member",
  "login_url": "https://notifuse.app.veridian.site/magic?token=..."
}
```

Idempotent (user déjà membre, même role) → 200 `already_member: true`.

**Toujours** un `login_url` (magic link auto-login Notifuse). Pattern
identique à `provision.login_url` existant.

### Codes erreur

Code | Status | Sens
---|---|---
`unauthorized` | 401 | HMAC invalide ou drift > 5min
`tenant_not_found` | 404 | `tenantId` n'existe pas
`invalid_body` | 400 | JSON parse fail ou champs manquants
`invalid_role` | 400 | role hors enum
`tenant_suspended` | 423 | Tenant suspendu (billing) — pas d'ajout
`user_role_conflict` | 409 | Membre existant avec autre role (préférer UPDATE)

### Idempotence et résolution de l'user

Pattern recommandé (mirror du `provision` existant) :

1. Lookup user Notifuse par `user_id = hub_user_id`.
2. Si absent : créer user Notifuse `{user_id: hub_user_id, email,
   type: 'user'}` (pas owner par défaut, juste un user humain).
3. Lookup `user_workspaces` (user_id, workspace_id).
4. Si présent avec même role → 200 already_member=true.
5. Si présent avec role différent → UPDATE role + audit log "via Hub".
6. Si absent → INSERT user_workspaces + audit log.

Cf bug désync user_workspaces de 2026-05-17 (mémoire
`reference_hub_notifuse_attach_owner_2026-05-17.md`) — ce nouveau
endpoint doit éviter le même piège : créer **toujours** la row
`user_workspaces` même si l'INSERT/UPDATE remonte une race.

### Tests obligatoires

- HMAC valide → 201
- HMAC invalide → 401
- Drift > 5min → 401
- Tenant inconnu → 404
- Idempotent re-call → 200 already_member=true
- User Notifuse inexistant → créé puis attaché
- Role conflict → UPDATE + audit
- Tenant suspended → 423

## Sécurité

- Aucun log clear du body (contient `hub_user_email`).
- 404 renvoyé **après** HMAC OK, sinon scan de tenantId énumérable.
- Rate-limit recommandé (60/min/IP) — défense en profondeur côté Notifuse.

## Lien avec le Hub

Une fois cet endpoint livré, l'agent Hub :

1. Câble le call HMAC dans `lib/invitations/accept.ts` (TODO P1-step4b).
2. Bascule la réponse Hub `/api/invitations/[token]/accept` 202 → 200.
3. Renvoie au front le `login_url` Notifuse au lieu du fallback générique.

Contrat global côté Hub :
`memory/reference_hub_invitation_hmac_contract.md`.

## Référence

- Spec P1 Hub : `veridian-hub/todo/2026-05-20-hub-invitation-endpoints.md`
- Helper accept Hub : `veridian-hub/lib/invitations/accept.ts` (TODO marker)
- Middleware HMAC Notifuse : `internal/http/middleware/veridian_hmac.go`
- Pattern endpoint similaire : `internal/http/veridian_handler.go`
  → `handleProvision`, `handleAttachOwner`
- Bug désync ref : mémoire Hub
  `reference_hub_notifuse_attach_owner_2026-05-17.md` (à éviter)

## Effort estimé

- 0.5j : handler Go + struct body + service `AttachMember`
- 0.5j : tests unitaires (HMAC, idempotence, role conflict)
- 0.5j : intégration E2E (mock Hub call → endpoint → DB)
- Total : ~1.5 jour

## Notes Go-spécifiques

- Pas de Prisma ; transaction via `internal/repository` (pattern
  `provision` existant).
- Logging structuré via `zerolog` (cf existant).
- Pas de TS Zod → struct Go + validation manuelle (cohérent avec
  `handleProvision`).
- Tests unitaires sous `internal/http/veridian_handler_test.go`.
