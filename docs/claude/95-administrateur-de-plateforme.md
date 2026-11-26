# 95 — Administrateur de plateforme : propriétaire en plus du client (25/11/2026)

Demande de Robert (25/11) : tout pouvoir comme propriétaire, sans l'être à la place du client, et pouvoir se faire retirer par lui. Le même soir, une
session avait lancé `admin/attach-owner`, qui TRANSFÈRE : le client d'un espace réel est passé membre. Code : `internal/veridian/platformadmin/`, V82.

## Les routes (HMAC Hub)
- `POST /api/veridian/admin/platform-admin` `{workspace_id, hub_user_id, granted_by, reason, dry_run}`, CLI `admin:platform-admin --data '{…}'`.
  Par `hub_user_id` seulement : une adresse ou un champ inconnu = 400. `state` : `added`, `promoted`, `already_admin` (rejeu, rien n'est écrit),
  `already_owner` (propriétaire en propre, jamais marqué). `before` et `after` : toutes les appartenances, sans adresse.
- `GET …/platform-admin?workspace_id=`, CLI `admin:platform-admin-list` : le relevé.
- Refus, rien d'écrit : 404 `tenant_not_found`, `person_unknown` (inconnue, supprimée au Hub, machine) ; 409 `removed_by_owner`, `no_client_owner`
  (aucun autre propriétaire humain), `others_changed` (une autre ligne a bougé pendant le geste : tout est annulé).

## Ce qui tient
- **Une seule ligne s'écrit.** Dans la transaction, espace verrouillé, rôle, droits et dates de toutes les autres appartenances sont comparés avant et
  après ; la date de la ligne de la personne ne bouge pas non plus.
- **Le client retire l'administrateur** par l'écran Équipe (route amont). **L'inverse est refusé** : `removeMember` et `setUserPermissions` d'un
  administrateur vers un propriétaire du client = 403 `client_owner_protected`. Situation non lue = 503, jamais un passage.
- **Un retrait n'est jamais remis tout seul** : marque présente et plus propriétaire = 409, même si la trace du retrait manque ; `sync-member` et
  `restore-member` du Hub aussi. Une invitation acceptée (`attach-member`) reste possible : c'est l'acte du client.
- « Le propriétaire » désigne le client : `transfer-owner` ne rétrograde jamais un administrateur à sa place, `tenants/health` annonce le client.
- Journal : `veridian_platform_admin` (qui, quand, pourquoi, rôle d'avant, retiré quand et par qui) et une ligne de log par geste, refus compris.

## Limites
Dans l'écran Équipe, l'administrateur paraît comme un propriétaire ordinaire : aucune étiquette. Il peut encore supprimer l'espace ou transférer par les
routes signées, comme tout propriétaire. Un emprunt de session (`issue-magic-link`) n'est toujours pas journalisé ici.

## Épreuves
`internal/veridian/platformadmin/*_test.go` (PostgreSQL réel), `internal/service/veridian_attach_owner_guard_test.go`,
`python3 scripts/e2e/administrateur-plateforme.py` (staging, espace jetable, 0/1/2).
