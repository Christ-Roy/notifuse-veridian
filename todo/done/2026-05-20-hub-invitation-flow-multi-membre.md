# [CROSS-APP] Support invitation Hub multi-membre côté Notifuse

> **Type** : Ticket cross-app, owner = agent Notifuse
> **Sévérité** : 🟡 P2 (pas bloquant tant que multi-membre Notifuse pas vendu)
> **Owner principal** : agent Notifuse
> **Owner secondaire** : agent Hub (fournit la spec)
> **Créé** : 2026-05-20 (session OAuth Sign-in Hub)

## Contexte

Depuis l'arrivée d'OAuth Sign-in Hub (Google + Microsoft) 2026-05-20, l'identité
utilisateur Veridian est centralisée côté Hub. Tout flow d'invitation
multi-membre côté app downstream (Notifuse, Prospection) doit transiter par
le Hub pour ne pas créer d'identités dupliquées.

Pour Notifuse, l'**owner d'un workspace** peut aujourd'hui inviter d'autres
membres directement via l'UI Notifuse (`POST /api/workspaces/:id/invite`).
Si l'invité n'a pas encore de compte Hub, on a le même problème que sur
Prospection : son user Notifuse est orphelin de Hub.

## Spec attendue

Identique à celle de Prospection (cf. `veridian-prospection/todo/...-prospection-invite-flow.md`) :
1. L'UI Notifuse "Inviter membre" doit appeler `POST app.veridian.site/api/invitations/create` (HMAC)
2. Le Hub gère le magic link et le signup/login de l'invité
3. Une fois loggué, le Hub appelle `POST notifuse/api/tenants/attach-owner` (qui existe déjà côté Notifuse depuis 2026-05-17) avec le `user_id` Hub

## Pré-requis

- ⏳ Le Hub doit d'abord livrer `POST /api/invitations/create` (cf. ticket Hub)
- ✅ L'endpoint `POST /api/tenants/attach-owner` existe déjà côté Notifuse

## Effort estimé

- 2-3j : modifier UI Notifuse "Inviter membre" + flow API
- 1j : retirer la table `notifuse_invitations` locale si elle existe
- 1j : tests d'intégration

## Bloque

- Vente du plan "Team" Notifuse (multi-membres payants)
- Cf. CONTRAT-HUB.md §6 (multi-membre payant)

## Référence

- Spec Hub : `veridian-hub/todo/integrations/2026-05-20-prospection-invite-flow.md`
- Contrat Hub : `docs/CONTRAT-HUB.md` §3 + §6

## Réponse — 2026-05-23 (livré par agent Notifuse, SHA 7f849c5a)

### Implémentation côté Notifuse

- **Client Go** : `internal/service/veridian_hub_invitation_client.go`
  - POST hub/api/invitations/create avec HMAC sha256 (ts + '.' + rawBody)
  - Headers : x-veridian-app=notifuse + x-veridian-timestamp + x-veridian-invitation-signature
  - Mapping erreurs Hub → `HubInvitationError` typée (hub_unreachable, inviter_not_found, invalid_payload, rate_limited, etc.)
  - 13 tests httptest server (HMAC vérifié bit-à-bit côté test)

- **Handler HTTP** : `internal/http/veridian_invite_member_handler.go`
  - Route : `POST /api/veridian/workspaces.inviteMember` (JWT requireAuth)
  - Flow : auth → AuthenticateUserForWorkspace (membership) → user.HubUserID (V46) ou fallback DB lookup → Call Hub → Map erreurs
  - 20 tests (mocks gomock auth + stubs client/resolver)

- **Config** : `HUB_BASE_URL` (default https://hub.veridian.site) + `HUB_INVITATION_SECRET_NOTIFUSE`
  - managedMode := (HUB_API_SECRET != "" && HUB_INVITATION_SECRET_NOTIFUSE != "")
  - Si l'un manque, endpoint renvoie 503 propre (pas de crash) + log warn

- **Front console** : `console/src/components/settings/WorkspaceMembers.tsx`
  - Détecte mode via /api/veridian/mode
  - En mode managed : appelle `workspaceService.inviteMemberViaHub`
  - En self-hosted : appelle `workspaceService.inviteMember` (upstream inchangé)
  - Branche aussi resend (Hub idempotent reused=true)
  - Messages d'erreur custom : "compte non lié au Hub" (422), "Hub indisponible" (502)
  - i18n catalogues re-extraits + compilés

### Validation live

```bash
TS=$(date +%s%3N)
SIG=$(printf "%s.%s" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | cut -d' ' -f2)
curl -X POST https://hub.staging.veridian.site/api/invitations/create \
  -H "Content-Type: application/json" \
  -H "x-veridian-app: notifuse" \
  -H "x-veridian-timestamp: $TS" \
  -H "x-veridian-invitation-signature: $SIG" \
  -d '{"inviter_user_id":"test","inviter_email":"x@x.com",...}'
# -> 404 inviter_not_found (HMAC ACCEPTE, payload Zod OK)
```

### ATTENTION : Secret prod ABSENT

`HUB_INVITATION_SECRET_NOTIFUSE` n'est configure **ni en prod** (compose `WN0jglLj5bDIrXUFZHNmw`) **ni en staging** (compose `compose-bypass-bluetooth-feed-tbayqr`). Tant que vide, l'endpoint renvoie 503 propre — la console front conserve le flow upstream local (notifuse_invitations) sur self-hosted.

**Action requise pour activer le flow** :
1. Generer un secret partage identique cote Hub + Notifuse (32 bytes random hex)
2. Injecter via Dokploy API `compose.update` env `HUB_INVITATION_SECRET_NOTIFUSE=<secret>` sur les 2 composes Notifuse
3. Idem cote Hub si pas deja fait (env `HUB_INVITATION_SECRET_NOTIFUSE` cote hub-prod + hub-staging)
4. Redeployer les 2 stacks

Tant que pas fait, l'endpoint /api/veridian/workspaces.inviteMember repond 503 + log warn au boot.

### Lien commits

- SHA principal : 7f849c5a (rebase sur 5e21d148)
- Branche : veridian (origin)
