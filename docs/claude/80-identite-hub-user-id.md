# Identité : le Hub est la source des utilisateurs (Robert, 06/11/2026)

Un utilisateur ne se crée, ne se modifie et ne se supprime qu'au Hub. Ici, `users` est une copie
alimentée par le Hub, jointe par `users.hub_user_id` = `hub_app.users.supabase_user_id` (UUID v4,
jamais `users.id`). Contrat : `veridian-hub/docs/CONTRAT-HUB.md` §3.7 et §6bis.8.

## Règles de `POST /api/sso/issue-magic-link`

1. `hub_user_id` d'abord (l'email local peut différer de celui du Hub), email ensuite.
2. Compte trouvé par email déjà lié à un AUTRE `hub_user_id` : 400 `hub_user_id_mismatch`, aucun
   lien (`ErrHubUserIDConflict`). Le Hub lit ce 400 comme un échec, pas comme `user_not_in_app`.
3. Compte sans lien : le repli par email connecte, mais `hub_user_id` n'est écrit que si le body
   porte `email_proven: true`. Le Hub ne l'envoie pas encore (`lib/auth/bounce-apps.ts` n'envoie que
   `hub_user_id` et `email`; il garde l'accès par `denyUnlessEmailProven` avant d'appeler). Tant
   qu'il ne l'envoie pas, ce chemin ne lie personne : le lien vient de `provision`
   (`metadata.hub_user_id`), d'`attach-member` et du backfill.
4. La session se retrouve par l'email LOCAL du compte, pas celui du body.

## Lien auto-login (`/veridian/auto-login`)

Usage unique : `ConsumeOnce` sur `veridian_idempotency_keys` (clé = sha256 de la partie signée).
Un même `$n` SQL ne doit jamais servir à deux colonnes de types différents (key VARCHAR,
request_hash CHAR) : Postgres refuse, sqlmock ne le voit pas (E2E staging rouge le 06/11).
Arrivée directe dans `/console/workspace/<id>`, jamais sur « Select workspace ».

## Schéma

Index unique partiel `idx_users_hub_user_id_unique` (V70), aussi dans
`internal/database/schema/system_tables.go` pour les installations neuves.
