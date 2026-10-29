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
dans `webhook_deliveries`. La charge utile ne porte JAMAIS le corps : `message_id`, `kind`, `email`, `thread_id`, `subject`,
`from`, `attachment_count`, `sent_message_id`. Une panne du webhook n'empêche jamais de ranger le mail.

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
