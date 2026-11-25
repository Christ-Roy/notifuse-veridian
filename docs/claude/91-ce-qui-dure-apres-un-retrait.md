# Ce qui dure après le retrait d'une personne (25/11/2026)

**Pourquoi** : mesuré par le Hub (cas 37, prod, 25/11) : un membre retiré ouvrait encore la boîte par IMAP, à 60 s, 8 essais sur 8, avec l'accès créé pour
son téléphone. Même classe, mesurée ensuite en staging sur l'ancien code (15 défauts) : SMTP d'envoi, flux, membre gelé, espace suspendu, compte supprimé.

## La règle
Tout ce qui dure plus qu'une requête relit la situation de la personne **à chaque passage** (`internal/veridian/standing`, une lecture) :

| Situation | IMAP et SMTP d'un accès créé par la personne | Flux et boîte unifiée | Réversible |
|---|---|---|---|
| retirée de l'espace | refusés ; accès **révoqués** par `remove-member` | flux fermé (`event: closed`), API 403 | non : un retour ne rouvre rien |
| gelée (§5.21) | refusés (une boîte ne se floute pas) | 403, et 402 en écriture | oui, même mot de passe |
| compte supprimé au Hub | refusés | 401 | non |
| espace supprimé (soft-delete) ou disparu | refusés, quel que soit l'auteur | 403 | oui |
| espace suspendu (§5.4, lecture seule) | IMAP lit, SMTP refuse (550) | lecture oui, envoi 403 | oui |

- Session IMAP déjà ouverte : coupée en 5 s au plus. Flux : 2 s au plus. Une panne de lecture n'ouvre rien et n'est jamais un refus d'identifiants.
- **Accès créé par une clé d'API** : il appartient à l'espace, comme la clé, et reste. `remove-member` rend `revoked_mail_accesses` (ceux de la personne)
  et `active_mail_accesses` (ce qui reste dans l'espace), à côté de `active_api_keys`. Absent = non compté.
- **Compte supprimé** (`users.hub_deleted_at`) : `internal/veridian/accountgate` enveloppe les dépôts. Aucune session ne s'ouvre (`user.signin`,
  `user.verify`, lien déjà émis), aucune ne vaut. Exception : `SystemCallKey`, la session interne d'un geste du Hub (transférer l'espace d'un supprimé).
- Un retiré avec un lien déjà émis ouvre une session, mais pas l'espace : l'appartenance est relue à chaque appel (403).
- Le filtre du gel n'exempte plus `/api/veridian/` : la console y a ses routes. Les routes du Hub portent une signature, pas un jeton d'utilisateur.
- Un espace effacé emporte ses accès (`veridian_box_access`).

## Épreuves
`standing_test.go`, `accountgate_test.go`, `guard_test.go`, `standing_test.go` (mailapi), `TestRemoveMember_*` : 23 sabotages vus rouges.
`python3 scripts/e2e/retrait-boite.py` (staging, vrais IMAP, SMTP et flux) : 0 prouvé, 1 défaut, 2 non abouti. En prod : cas 37 du Hub.
