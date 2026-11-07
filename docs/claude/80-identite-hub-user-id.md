# Identité : le Hub est la source des utilisateurs (Robert, 06/11/2026)

Un utilisateur ne se crée, ne se modifie et ne se supprime qu'au Hub. Ici, `users` est une copie
alimentée par le Hub, jointe par `users.hub_user_id` = `hub_app.users.supabase_user_id` (UUID v4,
jamais `users.id`). Contrat : `veridian-hub/docs/CONTRAT-HUB.md` §3.7 et §6bis.8.

## Règles de `POST /api/sso/issue-magic-link`

1. `hub_user_id` d'abord (l'email local peut différer de celui du Hub), email ensuite.
2. Compte trouvé par email déjà lié à un AUTRE `hub_user_id` : 400 `hub_user_id_mismatch`, aucun
   lien (`ErrHubUserIDConflict`). Le Hub lit ce 400 comme un échec, pas comme `user_not_in_app`.
3. Compte sans lien : le repli par email connecte, mais `hub_user_id` n'est écrit que si le body
   porte `email_proven: true`. Le Hub l'envoie depuis `hub:v0.5.45` (06/11, 22:30 UTC).
3bis. `email_proven` est envoyé par le Hub (v0.5.45, CONTRAT-HUB §3.8) sur issue-magic-link ET provision.
   `provision` : owner créé par l'appel = lié d'office ; compte existant trouvé par email = lié seulement
   si `email_proven`. Sans le champ l'appel réussit quand même (auto_login_url compris), sans lien.
4. La session se retrouve par l'email LOCAL du compte, pas celui du body.

## Lien auto-login (`/veridian/auto-login`)

Usage unique : `ConsumeOnce` sur `veridian_idempotency_keys` (clé = sha256 de la partie signée).
Un même `$n` SQL ne doit jamais servir à deux colonnes de types différents (key VARCHAR,
request_hash CHAR) : Postgres refuse, sqlmock ne le voit pas (E2E staging rouge le 06/11).
Arrivée directe dans `/console/workspace/<id>`, jamais sur « Select workspace ».

## Schéma

Index unique partiel `idx_users_hub_user_id_unique` (V70), aussi dans
`internal/database/schema/system_tables.go` pour les installations neuves.

## Routes d'identité du Hub (CONTRAT-HUB §3.8, 07/11/2026)

Module `internal/veridian/hubusers/`, sous HMAC Hub (sans signature valide : 401), câblé par
`internal/app/veridian_mod_hubusers.go`. CLI : `hub:users`, `hub:users-sync`.
- `GET /api/hub/users` : `{users:[{hub_user_id,email,name}]}`, humains seulement (`type='user'`),
  `hub_user_id` null pour un compte non relié, `users.id` jamais exposé. Alimente `hub reconcile`.
- `POST /api/hub/users/sync` `{hub_user_id,name?,email?,deleted?}` : compte retrouvé par `hub_user_id`
  SEUL (jamais par email), seuls les champs présents sont appliqués. 404 `user_not_in_app`, 409
  `email_conflict`, 400 si uuid ou email invalide.
- `deleted:true` : sessions coupées (`user_sessions`) et `users.hub_deleted_at` posé (V71) ; aucune
  donnée supprimée. `issue-magic-link` ne délivre plus de lien à ce compte (`user_not_in_app`).
  Limite connue : l'écran natif de connexion par code email reste ouvert, §3.8 interdit les écrans natifs.

## Connexion automatique par rebond (CONTRAT-HUB §6bis.8.0, 07/11/2026)

Console (`console/src/lib/veridian_auto_bounce.ts`, branché dans `RootLayout`) : une visite SANS session
sur une page protégée, en navigation complète et hors iframe, repart vers
`<hub>/api/auth/bounce/prepare?next=<page demandée>&auto=1`. Deux repères locaux (cookies de l'hôte de
l'app, jamais sur `.veridian.site`) : `veridian-auto-bounce` (5 min, un seul essai, au retour on montre la
connexion) et `veridian-logout` (posé par la page de déconnexion, plus aucun rebond silencieux). Les deux
sont levés par `/veridian/auto-login` (connexion réussie). Le bouton « Continuer avec mon compte Veridian »
de la page de connexion rappelle le rebond sans `auto`.
- `issue-magic-link` accepte `next` : suivi seulement s'il est sur l'origine de l'app
  (`domain.ConsoleNextPath`), ajouté au lien en `&next=<chemin>`. `/veridian/auto-login` le revalide
  (chemin `/console...` seul, jamais `/console/signin|logout|setup`) avant de rediriger, sinon workspace du lien.
- `POST /api/hub/users/sync` `revoke_sessions:true` : coupe les sessions du compte sans le toucher
  (`updated:true` s'il y en avait).
