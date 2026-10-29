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
