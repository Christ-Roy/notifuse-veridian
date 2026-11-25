# Régler sa boîte soi-même (25/11/2026)

**Pourquoi** : un client (25/11) avait réglé son application en STARTTLS sur 443, mot de passe de réception vide. Rien n'arrivait jusqu'à nous, donc aucun
journal ne le disait. Robert, le même jour, à propos des boîtes louées : tous les modes pour que ce soit simple à régler, et pouvoir exporter la boîte ailleurs.
**Portée** : tout espace. Réglages > Accès aux boîtes (console), API `mail.access.*`, CLI `mail:access-*` et `mail:export`.

| Besoin | Route | Règle |
|---|---|---|
| Désactiver, réactiver | `POST mail.access.setDisabled` (V81 `disabled_at`) | même mot de passe au retour ; connexions coupées ; la révocation reste définitive |
| Profil Apple | `POST mail.access.profileLink` rend une URL de 15 min ; `GET mail.access.profile?t=` (publique) | réglages seulement, JAMAIS de mot de passe ; l'appareil le demande une fois et s'en sert des deux côtés ; identifiants stables, réinstaller remplace |
| Test de connexion | `POST mail.access.test` | compare ce que la personne a saisi à `connection`, joint NOTRE porte (jamais l'adresse saisie), dit pourquoi un mot de passe juste n'ouvre plus ; 30 essais par 10 min |
| Derniers essais | dans `mail.access.list` (`attempts`, `attempts_since`) | en mémoire, bornés, remis à zéro au redémarrage ; « aucun essai reçu » = le réglage n'atteint pas la porte |
| Export | `GET mail.export` (mbox) | un à la fois par espace ; droit relu à chaque lot, perte de droit = connexion coupée, jamais un fichier tronqué |

- `closed_reason` : `holder_gone`, `address_removed`, `workspace_closed` (`Guard.Reason`, seule vérité).
- Le lien de profil est signé avec la clé de l'instance ; révoqué ou expiré = 404. Il tient sous 190 octets, tenu par test : au-delà de 213 l'écran n'a
  plus de QR (vu vide en staging le 25/11, lien de 220 octets).
- `connection` dit la sécurité comme un client mail (« SSL/TLS ») ; l'écran la lit par `securityCode`, jamais par `=== 'ssl'`.
- Écran : `console/src/components/settings/veridian_mailbox_*.tsx`. « Aucun essai reçu » n'est un diagnostic que 10 min après la création d'un accès.
  Porte muette = notre faute, dite comme telle. Captures relues le 25/11 (bureau, 390 px).
- Ports : `also` liste les autres réglages justes (fiche 93).
- `mail.access.test` répond par codes (`host`, `port`, `security`, `username`, `password_empty`, `door`, `credentials`, `access_open`, `can_send`,
  `not_suspended`, `profile_active`) ; la console les traduit.

## Épreuves
`boxapi/setup_test.go` (dont le cas d'origine), `boxaccess/attempts_test.go`, `mailapi/export_test.go` : 19 sabotages vus rouges.
