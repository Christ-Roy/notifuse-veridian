# 84. Standard des boîtes louées : une seule porte, Notifuse (23/11/2026)

**Origine (Robert, 23/11/2026)** : « Notifuse est exposé à Internet, il a ses propres accès, et Postfix, derrière le VPN, a
les siens. [...] y accéder sous différentes formes : méthode native, API, webhook. Il faut que ça soit standardisé pour tout
le monde. [...] L'application doit pouvoir se suffire à elle-même. » Déclencheur : les réponses de greenbeamcraft passaient
par Cloudflare Email Routing puis un libellé Gmail ; 25 réponses invisibles pendant six jours.
**Portée** : tout workspace, toute boîte. **Porte de sortie** : un besoin que le standard ne couvre pas se pose à Robert.

## La règle

Un client ne parle qu'à Notifuse. Aucun tiers (Cloudflare Email Routing, filtre ou libellé Gmail, alias Postfix, Sieve) ne
fait partie du chemin d'un mail. Ce qui est derrière (Postfix interne, Google, Infomaniak) est un détail interchangeable.

    prospect --> NOTIFUSE (MX, port 25 par Traefik) <-- IMAP / SMTP / API / webhook / écran -- client
    client  --> NOTIFUSE (SMTP 587) --(tailnet)--> relais du profil --> prospect

| Surface | Rôle | Accès | État au 23/11/2026 |
|---|---|---|---|
| Écran `/inbox`, API `mail.*`, SSE | lire, répondre, traiter | compte Hub, clé API du workspace | existe |
| Webhook `message.received` | prévenir un CRM | souscription du workspace | existe |
| Transfert par profil (5 adresses) | copie vers le client ou Robert | réglage du profil | existe, CLI et API seulement (78) |
| SMTP de soumission | envoyer depuis une appli ou un client mail | clé API | existe, tailnet seul, profil transactionnel seul (71) |
| IMAP | Thunderbird, téléphone, CRM | accès propre à la boîte | **n'existe pas** |
| MX | recevoir | aucun, public | animalsd : relais. greenbeamcraft et `agence(s)-veridian.fr` : Cloudflare, à sortir |

Deux jeux d'accès, jamais mélangés : ceux du **client** (créés, affichés une fois et révoqués dans Notifuse, par boîte) et ceux
de **Notifuse vers le relais** (chiffrés dans l'intégration, jamais montrés au client, joignables par le tailnet).

## Décisions

1. **Tout vit dans Notifuse, scopé par profil d'envoi** (Robert, 23/11 au soir) : IMAP, SMTP et réception. Postfix ne parle qu'à
   Notifuse, par le tailnet. La réception reste un processus séparé des déploiements de l'app (un arrêt ne fait que différer
   le courrier : les serveurs d'envoi réessaient). **Aucune bascule de MX ni ouverture publique sans son oui explicite.**
2. **L'IMAP est servi par Notifuse depuis la boîte unifiée** (`veridian_mail_message`), pas par un proxy vers Dovecot : le même
   accès marche quel que soit le fournisseur derrière, et le client voit exactement ce que voit l'API. Serveur :
   `emersion/go-imap/v2/imapserver` (même bibliothèque que le client déjà en service).
   **Scopé par l'adresse du profil, pas par `profile_id` seul** : mesuré le 23/11 sur greenbeamcraft, 24 réponses sur 159 adressées
   à jean-claude.benard@ sont rangées sous un autre profil (5) ou sous aucun (19). À corriger avant l'IMAP.
   **Aucun port nouveau pour lire et envoyer** : Traefik (443, déjà public) aiguille par nom TLS (`HostSNI`) vers le service Nomad
   de Notifuse, port dynamique, donc le conteneur peut bouger. Éprouvé le 23/11 en local avec le Traefik de la prod (3.7.17) :
   IMAP et site web sur le même port, mauvais mot de passe refusé, nom inconnu muet, backend déplacé repris en moins de 12 s.
   Option ALPN `imap` acceptée. Limite : Gmail « Envoyer en tant que » n'accepte que 25, 465 et 587.
   **Un seul port public neuf est inévitable : le 25 pour recevoir** (les serveurs de mail ne savent livrer que là), tenu par
   Traefik vers Notifuse, pas par le conteneur.
3. **Dossiers** : `INBOX` (reçus), `Sent`. UID = numéro d'ordre durable, UIDVALIDITY fixe par boîte. `\Seen` par accès, « traité »
   de la console inchangé. Aucune suppression définitive par IMAP en première version.
4. **SMTP côté humain** : un accès de boîte envoie par le profil de SA boîte, From imposé, Cc et Bcc compris, immédiat (pas de
   fenêtre ni de chauffe commerciales), avec plafond journalier par accès et les suppressions (`bounced`, `complained`).
5. **Transfert** : la seule façon de recevoir une copie ailleurs est le transfert du profil ; il gagne un écran.
6. **Exposition publique** seulement avec : TLS obligatoire, accès par boîte révocable, limite d'essais par IP et par compte,
   bannissement progressif, journal sans contenu, plafond d'envoi par accès (phase 2 de la fiche 71).

## Lots

| Lot | Contenu | Vu en prod quand |
|---|---|---|
| S0 (suspendu, attend Robert) | greenbeamcraft : MX vers Notifuse, transferts par profil, retrait de l'intégration Gmail | une réponse externe arrive dans `/inbox` et en copie, sans Cloudflare ni Gmail |
| S1 | Accès de boîte (créer, lister, révoquer) : API, CLI, écran ; écran des transferts | un accès créé puis révoqué est refusé |
| S2 | Rangement par adresse, serveur IMAP, tailnet d'abord, puis public par le 443 existant | Thunderbird lit et marque lu ; mauvais mot de passe banni |
| S3 | SMTP de boîte, public par le 443 existant | une réponse écrite dans Thunderbird arrive chez le prospect, DKIM pass, visible dans `/inbox` |
| S4 | `agence-veridian.fr` et `agences-veridian.fr` sortis de Cloudflare, puis fermeture des 587, 465 et 993 publics des relais | scan externe : seuls les ports de la décision 1 répondent |

S0 ne dépend pas des autres. S4 ferme des ports dont se servent Gmail « Envoyer en tant que » et le Dolibarr d'ASD : recenser les
usagers avant.
