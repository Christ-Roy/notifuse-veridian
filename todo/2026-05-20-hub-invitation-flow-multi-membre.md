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
