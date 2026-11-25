# Une personne du Hub est son `hub_user_id`, jamais son adresse (25/11/2026, CONTRAT-HUB §5.19.7)

**Pourquoi** : le Hub sait changer l'adresse principale de quelqu'un, et une adresse se recycle. Mail cherchait par l'adresse : un retiré gardait
l'accès, ou le geste touchait celui qui avait repris l'adresse. Le Hub avait donc interdit le changement d'adresse à qui a Mail (`APPS_PAR_ADRESSE`).

## La règle
- `hub_user_id` (UUID de 36 caractères) obligatoire : absent ou mal formé = 400 `invalid_payload` (`internal/http/veridian_hub_person.go`).
- Un seul résolveur, `internal/service/veridian_hub_member.go`. `hubMember` : par l'identifiant SEUL. L'adresse du corps (`email`, anciens noms
  `user_email` et `hub_user_email` acceptés) ne sert qu'à CRÉER une copie ; le compte trouvé garde la sienne, et c'est elle qui part dans les
  réponses, les liens de connexion et les événements (avec `hub_user_id`). Elle ne change que par `POST /api/hub/users/sync` (409 `email_conflict`).

| Route | Personne inconnue | Adresse portée par un autre compte |
|---|---|---|
| `remove-member` | 200 `removed:false`, rien n'est touché | ignorée |
| `restore-member`, `freeze-member`, `unfreeze-member` | 404 `user_not_in_app`, jamais de création | ignorée |
| `sync-member`, `attach-member` | copie créée ET reliée | relié ailleurs : 409 `hub_user_conflict` ; sans lien : relié si `email_proven`, sinon 409 |
| `transfer-owner` (`owner_hub_user_id`) | 404 ; pas membre : 409 `not_a_member` | ignorée |
| `provision` (`owner_hub_user_id`, sinon `metadata.hub_user_id`) | propriétaire créé et relié | relié ailleurs : 409 ; rejeu : même personne = 200 quelle que soit l'adresse, autre personne = 409 `owner_mismatch` |
| `by-email?hub_user_id=` | `found:false` | l'adresse n'est lue que sans `hub_user_id` |
| `workspaces.generateMagicLink` (clé d'espace gérée par le Hub) | 404 ; pas membre de CET espace : 404 | ignorée ; l'adresse seule (Hub d'avant) reste admise, pour un membre |

`attach-owner` et `provision` sans `hub_user_id` (scripts d'opérateur) désignent encore par l'adresse ; le Hub le donne toujours.

## Comptes d'avant, sans lien
Un compte sans `hub_user_id` n'est plus atteint par un geste du Hub tant qu'il n'est pas relié : par `issue-magic-link`, `sync-member`, `attach-member`
ou `provision` avec `email_proven`. `users/sync` ne relie pas (il cherche par l'identifiant seul). Compter, sans nommer : `notifuse-admin hub:users`
(`hub_user_id` null). 25/11 : 10 sur 30, dont 7 membres d'un espace vivant.

## Épreuves
`veridian_hub_member_test.go` : adresse changée puis reprise par un compte neuf, chaque geste ; vu échouer avec la recherche par adresse (remove,
restore, freeze, unfreeze), avec la fusion admise, avec le lien de connexion sur l'adresse reçue, avec le rejeu de provision par l'adresse.
`python3 scripts/e2e/personne-du-hub.py` (staging ; `--prod --ws <espace à nous>`) : 0 prouvé, 1 défaut, 2 non abouti.
