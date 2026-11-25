# Ce qui dure après le retrait d'une personne (25/11/2026)

**Pourquoi** : mesuré par le Hub (cas 37, prod, 25/11) : un membre retiré ouvrait encore la boîte par IMAP, à 60 s, 8 essais sur 8, avec l'accès créé pour
son téléphone, et son flux vivait encore à 86 s. Même classe, mesurée en staging sur l'ancien code : SMTP, membre gelé, espace suspendu, compte supprimé.
**Cible de la parente (25/11)** : un accès de personne tombe avec elle, connexions ouvertes comprises, avant le 200 ; un accès d'espace reste et est compté ;
rien ne se supprime, tout se rétablit à son retour ; aucun accès réel sans titulaire n'est touché.

## À qui appartient un accès (V80, `veridian_box_access.holder_user_id`)
- **Une personne** : elle le crée avec sa session. Il vaut tant qu'elle a sa place dans l'espace.
- **L'espace** (titulaire vide) : créé par une clé d'API, ou par le propriétaire avec `shared: true` (CLI `--shared`) ; un simple membre ne peut pas
  (403 `owner_required`), ce serait une porte qui lui survit. Pour la boîte d'un client ou un outil : toujours `--shared`.
- **Tous les accès d'avant V80 sont d'espace** : aucun n'a été rattaché après coup. Le rattacher est une décision de la parente, accès par accès.

## La règle
Tout ce qui dure plus qu'une requête relit la situation **à chaque passage** (`internal/veridian/standing`, une lecture) :

| Situation | IMAP et SMTP d'un accès de la personne | Flux et boîte unifiée | Au retour |
|---|---|---|---|
| retirée de l'espace | refusés, connexions coupées avant le 200 de `remove-member` | flux fermé (`event: closed`), API 403 | l'accès rouvre, même mot de passe |
| gelée (§5.21) | refusés, connexions coupées (une boîte ne se floute pas) | 403, et 402 en écriture | idem |
| compte supprimé au Hub | refusés (à partir de la date) | 401 | sans objet |
| espace supprimé ou disparu | refusés, accès d'espace compris | 403 | idem |
| espace suspendu (§5.4, lecture seule) | IMAP lit, SMTP refuse (550) | lecture oui, envoi 403 | l'envoi rouvre |

- `remove-member` rend `closed_mail_accesses` (ceux de la personne) et `active_mail_accesses` (ce qui reste ouvert dans l'espace), à côté de
  `active_api_keys`. Absent = non compté. `mail.access.list` dit `closed` et `holder_user_id`.
- Coupure : `boxaccess.CloseSessions` ferme les connexions IMAP (IDLE compris) ; chaque commande IMAP relit l'accès (1 s au plus), le SMTP à chaque
  destinataire et message. Une panne de lecture n'ouvre rien et n'est jamais un refus d'identifiants. `mail.access.revoke` coupe de même.
- Flux : relu à chaque passage (2 s), rien n'est envoyé sans droit lu.
- Liaisons et porte IA : la place dans l'espace est relue à chaque appel (`actions.Resolve`, `mcp.Resolve`). Un lien de connexion déjà émis ouvre une
  session, pas l'espace (403).
- Le filtre du gel n'exempte plus `/api/veridian/` : la console y a ses routes. Les routes du Hub portent une signature, pas un jeton d'utilisateur.
- **Compte supprimé** (`users.hub_deleted_at`) : `internal/veridian/accountgate` enveloppe les dépôts : aucune session ne s'ouvre (`user.signin`,
  `user.verify`, lien déjà émis) ni ne vaut. Exception : `SystemCallKey` (geste du Hub, transférer l'espace d'un supprimé reste possible).
  **Datée** (parente, 25/11) : seules les suppressions à partir de `VERIDIAN_ACCOUNT_GATE_SINCE` (RFC 3339, posée dans le job) coupent. Les 2 comptes
  d'avant (08/11) restent ouverts jusqu'à la décision de Robert, comptés dans `GET /api/hub/users` (`deleted_gate_since`, `deleted_still_open`) et dits au
  démarrage. Reculer la date les ferme. Absente en prod reliée au Hub, ou illisible : l'application refuse de démarrer, la version d'avant reste servie.
- Un espace effacé emporte ses accès. Retour arrière : l'image précédente ignore `holder_user_id`, rien à défaire.

## Épreuves
`standing_test.go`, `accountgate_test.go`, `guard_test.go`, `standing_test.go` (mailapi), `server_test.go` (imapgate), `TestRemoveMember_*`,
`TestFreezeMember_Cuts*` ; chaque garde sabotée et vue rouge. `python3 scripts/e2e/retrait-boite.py` (staging, vrais IMAP, SMTP, IDLE et flux,
60 s, témoins) : 0 prouvé, 1 défaut, 2 non abouti. En prod : cas 37 du Hub.
