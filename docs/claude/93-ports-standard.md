# 93. Ports standard des boîtes (993, 465, 587) : prêts, fermés (25/11/2026)

Demande (parente, 25/11) : « prépare tout (entrée, certificat, limites, repli), sans ouvrir ; Robert tranche ». **Rien n'est ouvert.**
Pourquoi : sur le 443, une application qui propose STARTTLS ou qui impose 993/465 échoue (Ben, 25/11 ; Gmail « Envoyer en tant que »).
Portée : toutes les boîtes. Porte de sortie : le 443 reste servi, aucun client existant n'a rien à changer.

## Mesures du 25/11/2026

| Port public de 51.210.7.44 (imap., smtp.veridian.site) | Tenu par | Usage relevé, 7 jours (journaux du relais, `obs logs mail-relay-nord`) |
|---|---|---|
| 993 | personne (fermé en public ; le relais l'a sur le tailnet seul) | sans objet |
| 465 | Postfix `mail-relay-nord` | **0 session authentifiée** |
| 587 | Postfix `mail-relay-nord` | 1 289 : 1 287 depuis le nœud (Notifuse lui-même), 2 depuis le bastion le 24/11 |

Conséquence : 993 est libre, 465 se libère sans gêner personne, **587 ne se libère pas seul** : Notifuse joint le relais par
`smtp.messagerie-nord-776.fr:587`, qui pointe sur cette même adresse (nom de HELO, à ne pas déplacer).

## Étapes, chacune avec son repli (exécutant : infra)

1. **993.** `ingress-ovh` : point d'entrée `imaps` sur `51.210.7.44:993` (adresse explicite : le 993 du tailnet est au relais), port déclaré
   dans `public_ports`. `notifuse-mx-entree` : `entrypoints=websecure,imaps` sur le routeur `notifuse-imap`. Repli : retirer les deux lignes.
2. **465.** `mail-relay-nord` : `submissions` passe en `host_network = "tailscale"` (comme son 993), `public_ports = "587"` ; puis point
   d'entrée `submissions` sur `51.210.7.44:465` et `entrypoints=websecure,submissions` sur `notifuse-boxsmtp`. Repli : l'inverse.
3. **Notifuse annonce les ports.** `VERIDIAN_IMAP_PUBLIC_PORT=993`, `VERIDIAN_IMAP_ALSO=443/ssl`, `VERIDIAN_BOX_SMTP_PUBLIC_PORT=465`,
   `VERIDIAN_BOX_SMTP_ALSO=443/ssl`. Écran, profil Apple et test de connexion suivent ; l'ancien 443 reste jugé juste. Repli : retirer.
4. **587 (déconseillé).** Code prêt, éteint : `VERIDIAN_BOX_STARTTLS_PORT` ouvre une porte STARTTLS (`boxsend.EnableStartTLS` : ni AUTH ni envoi
   avant TLS), le certificat géré prend alors le nom `smtp.veridian.site`. Il faut d'abord que Notifuse joigne le relais par le tailnet
   (`extra_hosts` du job, jamais le DNS). Gain nul : tout client courant sait faire 465.

**Certificat** : rien à émettre pour 993 et 465 (`*.veridian.site`, TLS terminé par Traefik). **Limites** : inchangées, elles sont dans HAProxy
(par /64 : 50 connexions et 120 par minute en IMAP, 20 et 60 en SMTP) et dans Notifuse (essais par adresse et par boîte) : elles suivent la vraie
adresse (PROXY v2), quel que soit le port. **Alerte** : `vrd-notifuse-boites-forcage` existe. **Coupure** : 4 à 8 s par redémarrage d'entrée.

## Épreuve après ouverture (0 / 1 / 2)

`openssl s_client -connect imap.veridian.site:993` puis `smtp.veridian.site:465`, depuis trois machines, v4 ; bannière « * OK » et « 220 ».
Accès de test : connexion bonne, mauvais mot de passe refusé, plafond par adresse atteint puis rendu, révocation qui coupe. Témoins : le 443
répond toujours, Ben reste connecté, le relais reçoit toujours les envois de Notifuse (587), le 25 répond.

## Décision de Robert

Ouvrir 993 et 465 (recommandé : mêmes portes, mêmes limites, deux ports qui attirent plus de robots), laisser 587 fermé. Autodécouverte
(SRV `_imaps`, `_submissions`, et `autoconfig`) : possible ensuite, elle publie les noms d'hôte, pas les boîtes.
