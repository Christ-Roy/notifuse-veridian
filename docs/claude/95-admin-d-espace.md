# 95 — Le rôle « admin » d'un espace (VER-263, 25/11/2026)

Robert, 25/11 : « je dois être admin partout », « y'a pas de mode admin où je peux tout faire comme propriétaire et me faire virer par ce dernier ? »,
« c'est à Notifuse de faire en sorte qu'on puisse être admin sans devoir devenir propriétaire ».

## La règle
`user_workspaces.role` vaut `owner`, `admin` ou `member`. Un admin a tous les droits d'un propriétaire SAUF supprimer l'espace, transférer la propriété,
retirer ou modifier un propriétaire. **Un seul point de décision** : `domain.UserWorkspace.VeridianAdministers()` (propriétaire ou admin) et `VeridianOwns()`
(`internal/domain/veridian_workspace_role.go`) ; console : `console/src/lib/veridian_workspace_role.ts`. Ne plus écrire `Role == "owner"` pour un droit.
Restent au propriétaire seul : `DeleteWorkspace`, `TransferOwnership`, les méthodes internes `AddUserToWorkspace` et `RemoveUserFromWorkspace` (un ajout
réécrit le rôle : ouvert à l'admin, il rétrograderait un propriétaire). Les alertes système vont au propriétaire seul. Une clé API n'est jamais admin.

## Qui le pose
`POST /api/hub/workspace-admins` (HMAC Hub, `internal/veridian/wsadmin`) `{workspace_id, hub_user_id, action: grant|revoke, actor_hub_user_id, reason,
reinstate}`. Jamais d'adresse (champ inconnu = 400), rien n'est créé : la personne doit être membre. `UserWorkspace.Validate` refuse toujours `admin`, donc
aucun ajout local n'en crée un. `GET ?workspace_id=` relit les rôles. 404 `user_not_in_app`, `not_a_member`, `tenant_not_found` ; 409 `admin_withdrawn`,
`is_owner`. Désigner un propriétaire ne change pas son rôle : la désignation est notée et, s'il cède la propriété, il devient admin dans la même écriture.

## Un retrait n'est jamais défait tout seul
V82 : `veridian_workspace_admins` (désignations) et `veridian_workspace_admin_events` (journal, refus compris). Rien à noter au retrait : une désignation
dont la personne ne tient plus le rôle, par quelque chemin que ce soit, EST un retrait (`VeridianAdminWithdrawn`). Alors `sync-member`, `restore-member`, la poussée
d'organisation, `attach-member` sans `invitation_id`, le copropriétaire configuré et un nouveau `grant` refusent. Une invitation acceptée ramène comme membre ;
seul `reinstate: true` (geste humain) redonne admin. Registre illisible : on n'ajoute pas.

## attach-owner ne rétrograde plus personne en silence
409 `owner_exists` si un autre propriétaire humain existe, avant toute création, sauf `transfer: true` (CLI `admin:attach-owner --transfer --yes`). Le
25/11 à 22:46 un rattachement avait rétrogradé un client. `transfer-owner` (Hub) est un transfert par nature.

## CLI et preuve
`notifuse-admin workspace-admins:grant|revoke|list` (simulation sans `--yes`). Le jeton opérateur d'un espace prend notre propriétaire, puis notre admin,
le compte d'un client en dernier recours. `python3 scripts/e2e/role-admin.py [--prod]` : matrice réelle propriétaire / admin / membre, espace jetable.
L'image précédente lit `admin` comme un membre à tous droits : revenir en arrière est sûr.
