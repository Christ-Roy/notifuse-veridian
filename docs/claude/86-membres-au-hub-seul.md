# 86 — Un membre ne s'ajoute qu'au Hub (24/11/2026)

Règle de Robert (06/11/2026) : un utilisateur ne se crée, ne se modifie et ne se supprime qu'au Hub. Code : `internal/veridian/membergate/`.

## Routes amont fermées (403 `members_managed_by_hub`, avec `hub_url`)

- `POST /api/workspaces.inviteMember` : ajoutait tout de suite un compte existant, sinon posait une invitation locale.
- `POST /api/workspaces.acceptInvitation` : publique à jeton, créait l'utilisateur et l'ajoutait.

Le fichier amont n'est pas touché : le motif « POST /chemin » gagne sur « /chemin » (ServeMux). Actif seulement si `HUB_API_SECRET` est
posé ; une installation sans Hub garde l'amont. Les autres méthodes vont à l'amont, qui répond 405. Mesuré avant de fermer : 0 invitation
en base, 0 appel sur 14 jours de journaux (témoin : `createAPIKey` vu).

## Le chemin qui reste

Écran Membres ou `notifuse-admin members:invite` -> `POST /api/veridian/workspaces.inviteMember` -> Hub `POST /api/invitations/create` (HMAC
`HUB_INVITATION_SECRET_NOTIFUSE`, même valeur des deux côtés, fiche 46) -> mail du Hub -> acceptation au Hub -> `attach-member` signé.
- Le secret avait disparu à la bascule vers Nomad : la route répondait 503 et l'écran retombait sur l'invitation locale. Il vit dans
  `nomad/jobs/notifuse/stack` et `nomad/jobs/notifuse-staging/stack`. Absent : 503, l'écran dit « Hub indisponible », aucun repli local.
- Staging appelle le Hub de staging (`HUB_BASE_URL`), pas celui de prod.
- Le Hub attache avec tous les droits : l'écran ne montre plus de tableau de droits à l'invitation. Restreindre ensuite (`setUserPermissions`).

## Les portes, toutes connues

`TestPortes_ToutAjoutDHumainEstConnu` lit le code et refuse une porte neuve (création d'utilisateur, ajout à un espace), la nôtre ou celle
d'une synchronisation amont. Restent : routes signées par le Hub, `setup.initialize` (refusée une fois installé), copropriétaire configuré
(`VERIDIAN_DEFAULT_CO_OWNER_EMAIL`), `createAPIKey` (machine). `signin`, `verify` et `rootSignin` ne créent personne. Modifications locales
encore possibles sans le Hub : `setUserPermissions`, `removeMember` (aussi la révocation d'une clé API), l'outil opérateur qui a le secret.
