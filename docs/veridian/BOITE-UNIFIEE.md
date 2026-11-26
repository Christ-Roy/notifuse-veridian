# Boîte unifiée (lot L1, V63)

Chaque mail envoyé et reçu par un workspace est gardé en entier dans `veridian_mail_message` (base du workspace),
rangé dans son fil, lisible par l'API. Code : `internal/veridian/mailbox/` (module `mailbox`, sous-paquets `mailapi`
pour l'API HTTP et `module` pour le câblage). Contrat de données : `plateforme/DESIGN-ET-ROADMAP.md` 3.2.

## Principe de panne

Notifuse LIT les boîtes IMAP des relais avec un repère (dernier UID traité, table `veridian_mail_sync_state`) : il n'est
pas dans le chemin du courrier. Arrêt ou panne : au redémarrage la lecture reprend à l'UID suivant, rien n'est perdu ni
relu. Une renumérotation du serveur (UIDVALIDITY) ne crée pas de doublon (déduplication par Message-ID dans la boîte).

## Qui écrit

| Source | Quand | Contenu |
|---|---|---|
| Syncer (`syncer.go`, appelé par le poller IMAP existant) | à chaque passage du poller, par lots de 25 messages ou 24 Mo | mails reçus : message brut chiffré, texte, HTML assaini, pièces, DSN décodé |
| Capture d'envoi (`observer.go`, crochet `queue.SetVeridianSentObserver`, une ligne dans `worker.go`) | à chaque envoi accepté par le relais | rendu exact : objet, HTML, texte, en-têtes de fil (`body_source = captured`) |
| Reprise de l'historique (`history.go`) | par lots de 150 envois de `message_history`, curseur durable | rendu reconstitué depuis le modèle d'époque (`rerendered`) ou `unavailable` ; une capture remplace toujours une reconstitution |

Aucune tâche planifiée ni daemon de plus : tout vit dans le tick du poller IMAP existant.

## Colonnes

Chiffrées au repos (clé du workspace, comme `message_data`) : `body_text`, `body_html_sanitized`, `raw_eml`.
En clair : adresses, objet, dates, références (indexables). Aucun contenu de mail ne va dans un journal.

`kind` : `sent`, `reply_human`, `reply_auto`, `bounce_hard`, `bounce_soft`, `dsn`, `other`.
`direction` : `out`, `in`. Un mail sans lien avec un de nos envois (aucune référence connue, expéditeur inconnu) est `other` ;
un rebond reste un rebond. `link` (jsonb) : `sent_message_id` (= `message_history.id`), `automation_id`, `node_id`,
`broadcast_id`, `match_type` (`message_id` ou `sender`). `dsn` (jsonb) : `code`, `severity`, `action`, `diagnostic`, `recipient`,
`provider`, `reason_fr`, `advice_fr`, `category`, `original_message_id`. `mailbox_*` : boîte IMAP, dossier, UIDVALIDITY, UID.

## Fils

`thread_id` se calcule sans base quand c'est possible : racine = première référence, sinon `In-Reply-To`, sinon le message
lui-même, sinon objet normalisé + contact. À l'arrivée, un mail qui cite un message déjà stocké hérite de son fil
(les clients qui coupent `References` restent dans le bon fil) ; un parent qui arrive après ses enfants rapatrie leur fil.

## Webhook `message.received`

Déclencheur SQL sur `veridian_mail_message` (mails reçus uniquement) : une livraison par souscription active abonnée,
dans `webhook_deliveries`. La charge utile ne porte JAMAIS le corps. Une panne du webhook n'empêche jamais de ranger le mail.

### Contrat (V77, V85, VER-247)

POST, en-têtes Standard Webhooks : `webhook-id` (= `id`, stable entre les réessais : clé d'idempotence), `webhook-timestamp`,
`webhook-signature: v1,<base64 HMAC-SHA256("id.timestamp.corps")>`, secret `whsec_<base64>`. 10 essais : 30 s, 1, 2, 5, 15, 30 min, 1, 2, 6, 24 h,
sans ordre garanti. Exemple réel anonymisé :
```json
{"id":"<livraison>","type":"message.received","workspace_id":"exemplews","timestamp":"2026-11-24T16:49:52Z","data":{
 "message_id":"<uuid Notifuse>","rfc_message_id":"abc@mail.exemple.test","kind":"reply_human","match_type":"message_id","direction":"in",
 "profile_id":"<uuid>","email":"contact@exemple.test","from":"contact@exemple.test","from_name":"Marie Dupont","subject":"Re: ...","thread_id":"t_<16 hex>",
 "sent_message_id":"exemplews_<uuid>","attachment_count":0,"event_timestamp":"2026-11-24T16:49:50.927791+00:00"}}
```
- `kind` : `reply_human` (un humain), `reply_auto` (absence, accusé automatique), `bounce_hard`, `bounce_soft` (rebond : `from` = mailer-daemon,
  `email` = le contact en échec), `dsn` (remis, relayé, retardé), `other` (sans rapport avec un envoi).
- **Une réponse à traiter = `kind = reply_human` ET `match_type` dans (`message_id`, `subject`).** `sender` = un contact connu qui écrit sans
  répondre à un envoi ; `null` = non rattaché. `sent_message_id` peut manquer sur une vraie réponse (elle cite un fil, pas l'envoi).
- **Un événement par mail** : ni copie Sieve, ni second rangement du même Message-ID par une autre boîte. Deux boîtes qui rangent à la même
  seconde peuvent encore en faire deux, et un mail sans Message-ID exploitable (`rfc_message_id` null) n'est pas regroupé : dédoublonner par
  `(workspace_id, rfc_message_id)`, sinon `message_id`.
- `from_name` (V85, 26/11/2026) : le nom affiché de l'en-tête From, chaîne vide sans nom. Le Hub en nomme la fiche CRM créée par une réponse.
- `timestamp` change à chaque réessai, `event_timestamp` non. Objet et adresses y sont : données personnelles.

## API (jalon 2)

Routes `/api/veridian/mail.*` (JWT utilisateur ou clé d'API ; spécification : `openapi/paths/veridian-mail.yaml`, CLI : `notifuse mail:*`).
Lecture : droit `message_history:read` ; `mail.messages.update` : `message_history:write` ; `mail.messages.send` et `reply` : en plus `transactional:write`.

| Route | Rôle |
|---|---|
| `mail.mailboxes.list` | une boîte par profil d'envoi et par adresse : compteurs, état de la lecture IMAP |
| `mail.messages.list` | filtres boîte, contact, fil, nature, sens, traité, dates, recherche (objet, adresses ; `body=true` cherche aussi dans les corps déchiffrés des 1500 derniers) ; pagination par curseur |
| `mail.messages.get` | message entier : en-têtes, texte, HTML assaini, pièces, DSN, lien. Jamais le message brut |
| `mail.messages.raw` | RFC 822 (un mail envoyé est reconstitué et l'en-tête `X-Veridian-Reconstructed` le dit) |
| `mail.messages.attachment` | contenu d'une pièce, téléchargement forcé, `nosniff`, CSP `sandbox` |
| `mail.messages.update` | traité / à traiter (`handled_by` = `agent:<clé>` ou `humain:<id>`), libellés |
| `mail.threads.get` / `list` | un fil complet (jalons : Premier mail, Relance n, Réponse, Rebond), ou tous les fils d'un contact |
| `mail.events` | flux SSE des nouveaux messages. Chaque événement porte son numéro d'ordre (`id:`) : reprise exacte avec `Last-Event-ID` ou `since_seq`, rien n'est perdu entre deux connexions. 8 flux au plus par workspace, reconnexion demandée toutes les 5 min |
| `mail.messages.send` / `reply` | met le mail dans la file des envois (`source_type = mail`) : le worker lui applique les MÊMES portes que les séquences (fusible de réputation, plafonds profil, classe de destinataire et adresse émettrice, chauffe, fenêtre, cadence). Refus immédiat si profil en pause, contact désinscrit, plainte ou rebond définitif. Une réponse part par le profil qui a reçu, avec `In-Reply-To` et `References`, avant les relances |

Le mail envoyé apparaît dans le fil dès que le relais l'a accepté (capture du worker).

## Copies et doublons (L1c, V68)

Un relais peut copier chaque vraie réponse vers une autre boîte que Notifuse lit aussi (filtre Sieve `redirect :copy`
vers la boîte de Hugo ; avant le 29/10/2026, un alias Postfix qui copiait même les rebonds). La copie garde le même
`Message-ID` : sans traitement, le mail est rangé deux fois et la console l'affiche deux fois.

- **Clé de regroupement** (`dedup_key`, entrants seulement) : `mid:` + Message-ID en minuscules s'il est exploitable
  (contient `@`, sans espace) ; sinon `fp:` + empreinte (contact, fil, date d'envoi, corps) ; sinon NULL, jamais regroupé.
- **L'original reste visible, la copie pointe l'original** (`duplicate_of`), n'est jamais supprimée et reste lisible par son
  id (`mail.messages.get`, `mail.messages.raw`). Le choix ne dépend ni de l'ordre d'arrivée ni des identifiants, il est
  recalculé sur tout le groupe à chaque arrivée. Classement : sans `X-Sieve-Redirected-From`, puis livré à l'un de ses
  destinataires (`Delivered-To` dans To/Cc : une copie par alias est livrée à une adresse qui n'est pas destinataire),
  puis reçu le plus tôt, puis `seq`.
- **Invisible** dans `mail.messages.list` (sauf `include_duplicates=true`), les compteurs des boîtes, `mail.threads.*`, le flux
  `mail.events` (qui ignore aussi toute copie Sieve) et le webhook `message.received` (le déclencheur SQL ignore une copie Sieve).
- **Traité** suit le groupe : `mail.messages.update` sur une copie ou sur l'original agit sur les deux ; une copie traitée avant
  l'arrivée de l'original donne un original traité.
- **Rattrapage** : la migration V68 marque les doublons existants (aucune suppression, aucun sortant touché). Le syncer rejoue le
  marquage (`SettleDuplicates`) au premier passage de chaque workspace après démarrage et après chaque passage qui a rangé du neuf
  (mails rangés par la version précédente pendant un déploiement glissant, ou résolution échouée).
- Le SQL du classement vit à un seul endroit : `internal/veridian/mailbox/dedupsql`. Un faux positif de comptage à connaître :
  deux mails périodiques de même objet et même début (notifications Meet, WeTransfer) sont des messages distincts, pas des doublons.
