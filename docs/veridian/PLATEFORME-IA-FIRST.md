# Notifuse Veridian : plateforme de prospection IA first (design et feuille de route, 29/10/2026)

Décideur : Robert. Auteur : session ASD Prospection. Base : audits du 27/10 (`architecte/RAPPORT-ARCHITECTE.md`, `audit-ux/`, `upstream/RAPPORT-UPSTREAM.md`) et mesures de prod du 28 et 29/10.

## 1. Vision en une page

Un logiciel de prospection où **l'agent IA est l'opérateur** et **l'humain regarde en dix secondes** ce qui se passe, sans travailler.

- **Tout se pilote par API, CLI et (étape 2) serveur MCP** : un agent range, crée, déplace, règle, lit les réponses, sans écran.
- **Tout se lit d'un coup d'œil** : une page « Aujourd'hui » dit ce qui part, ce qui répond, ce qui bloque, ce que l'humain doit faire (souvent rien), en français courant.
- **Des règles par défaut intelligentes** : un nouvel espace envoie bien sans réglage (plafonds par fournisseur destinataire, chauffe, ralentissement sur rebonds, répartition selon le stock réel).
- **Une boîte unifiée par API** : chaque mail envoyé et reçu (réponse, rebond, accusé) se lit en entier, mis en forme, dans son fil de conversation, comme dans un client IMAP, et un agent peut en envoyer comme avec SMTP, sans jamais quitter l'API.
- **Réconciliateur** : le logiciel recoupe en continu ce qu'il croit (compteurs, statuts) avec ce qui s'est réellement passé (journaux des relais, boîtes, accusés), signale chaque écart avec sa cause et propose la réparation.
- **Modulaire** : le code Veridian ne vit plus dans les fichiers upstream. Chaque capacité a une interface, un module, des tests.

## 2. Constats qui commandent l'architecture

1. Fork à 422 commits de l'upstream, 337 fichiers touchés des deux côtés : **pas de rebase**. Port ciblé uniquement. Les versions upstream ≥ 40 sont sous licence BSL (plus de 3 workspaces, permissions fines, audit : clé payante) : **pas de montée de version sans accord de Robert**.
2. Le câblage Veridian est mêlé au code upstream (`app.go` 274 occurrences, `worker.go`, `workspace_service.go`) : chaque nouvelle fonction ajoute de la dette. **Le socle modulaire passe avant tout.**
3. Notifuse relève déjà en IMAP les boîtes des relais, mais ne garde que le signal « a répondu » (`veridian_contact_reply`), jamais le texte.
4. Le rangement (segments, listes, modèles) repose sur une convention de nom `Dossier / Sous-dossier / Nom` : fragile, invisible pour l'API.
5. Le débit effectif dépend de trois portes (chauffe du domaine, plafonds par classe, stock par classe). Aujourd'hui les plafonds sont des constantes et le stock se mesure à la main (`stock-par-classe.py`, `profiles:gaps`).
6. Parité API/CLI : cinq trous bloquants pour un agent (lecture des inscrits d'une automation, profil d'envoi par campagne, priorité, désabonnement, annulation d'entrée de file).
7. CI : runner auto-hébergé lent (25 à 45 min par push), chaque push annule le précédent : **les livraisons passent par un seul train**.

## 3. Architecture cible

```
Relais postfix (5)  ──IMAP──►  Notifuse  ◄──API/CLI/MCP──  agents IA
 boîtes = source de vérité       │  ▲                           
 (rien ne se perd si Notifuse    │  └── console « Aujourd'hui » (humain, lecture rapide)
  tombe : rattrapage par UID)    ├── Boîte unifiée (tous les mails entrants et sortants, texte complet, fils) ──webhook──► CRM Dolibarr, autres
                                 ├── Réconciliateur (écarts entre compteurs, journaux relais, boîtes, statuts)
                                 ├── Dossiers (segments, listes, modèles : arbre natif)
                                 ├── Politique d'envoi (défauts par classe, stock-aware, priorités)
                                 └── Stock (jours de couverture par classe, besoins de réapprovisionnement) ──► chasse (ODH)
```

Principe de panne : Notifuse **lit** les boîtes avec un repère, il n'est pas dans le chemin du courrier. S'il tombe, rien n'est perdu et le retard se rattrape.

### 3.1 Règles de modularité (obligatoires pour tous les lots)

1. Nouveau code Veridian dans `internal/veridian/<module>/` ou `pkg/veridian_*`, jamais dans un fichier upstream, hors une ligne de branchement.
2. Fichiers gelés (aucune logique Veridian de plus) : `internal/app/app.go`, `internal/domain/workspace.go`, `internal/service/workspace_service.go`, `internal/domain/email_provider.go`, `internal/repository/message_history_postgre.go`, `internal/service/queue/worker.go`, `console/src/services/api/workspace.ts`, `internal/migrations/v32.go`.
3. Un seul fichier de câblage Veridian (`internal/app/veridian_wiring.go`) appelé d'une ligne depuis `app.go`.
4. Migrations Veridian : registre séparé (`internal/migrations/veridian/`), numéros réservés, expand puis contract, toujours additives.
5. Console : composants `veridian_*`, aucune édition d'un composant upstream hors une ligne d'intégration.
6. Toute fonction exposée en API l'est aussi en CLI (`notifuse`) avec la même sémantique, et documentée dans l'OpenAPI.
7. i18n : `.po` et `.js` générés, jamais édités à la main.


### 3.2 Contrat de données de la boîte unifiée (commun à L1 et L1b)

Table `veridian_mail_message` (une ligne par mail entrant ou sortant) :
`id`, `direction` (out, in), `kind` (sent, reply_human, reply_auto, bounce_hard, bounce_soft, dsn, other), `workspace_id`, `profile_id` (profil d'envoi ou boîte IMAP),
`mailbox_ref` (boîte source et UID IMAP, pour le rattrapage), `message_id`, `in_reply_to`, `references[]`, `thread_id` (calculé : références, sinon objet normalisé + contact),
`contact_email`, `from`, `to[]`, `cc[]`, `subject`, `sent_at`, `received_at`, `headers` (jsonb), `body_text` (chiffré), `body_html_sanitized` (chiffré), `raw_eml` (chiffré, option),
`attachments` (jsonb : nom, type, taille, empreinte ; contenu à la demande), `dsn` (jsonb : code, diagnostic, fournisseur, raison en français), `link` (jsonb : `sent_message_id` de `message_history`, automation, nœud, broadcast),
`handled_at`, `handled_by` (humain ou agent), `labels[]`. Index sur (`contact_email`, `received_at`), (`thread_id`), (`message_id`), (`kind`, `received_at`).
Les mails sortants viennent de `message_history` (rendu exact tel qu'envoyé, relié par `message_id`), les entrants du consommateur IMAP existant.

Table `veridian_reconcile_finding` : `id`, `check` (code de l'invariant), `severity`, `subject_ref`, `expected`, `observed`, `explanation` (français), `proposed_action` (jsonb exécutable par API), `first_seen_at`, `last_seen_at`, `resolved_at`, `resolved_by`.

### 3.3 API « mail » inspirée de JMAP (JSON sur HTTP), pas d'IMAP ni de SMTP à exposer

`mailboxes.list` (une boîte par profil et par adresse), `messages.list` (filtres : boîte, contact, fil, kind, traité, dates, recherche plein texte), `messages.get` (en-têtes, texte, HTML assaini, pièces jointes), `messages.raw` (RFC 822), `messages.update` (traité, libellés),
`threads.get` (le fil complet d'un contact : J0, relances, réponse, rebond, dans l'ordre), `messages.send` et `messages.reply` (envoi par un profil, mêmes plafonds et mêmes règles que les séquences, adressage dans le fil avec `In-Reply-To`),
`events` (flux serveur des nouveaux messages, remplace le webhook pour les agents), webhook sortant `message.received`. Même surface en CLI (`mail:*`) et plus tard en MCP. La conformité JMAP complète (RFC 8621) reste un objectif de second rang.

### 3.4 Invariants du réconciliateur (version 1)

1. **Envoyé = livré ou expliqué** : chaque mail sortant a, sous 48 h, un résultat (accepté par le relais, rebond, refus de politique) ; sinon écart « sans issue ». Source : `message_history`, journal Postfix des relais (par `message_id`), DSN reçus.
2. **Réponse rattachée** : chaque entrant humain est relié à un envoi (références, sinon contact et objet) ; sinon « orphelin » listé avec le contact probable.
3. **Statut cohérent** : un contact qui a répondu ou rebondi dur n'est plus inscrit à une séquence active ; un désinscrit ne reçoit rien ; un contact exclu n'est jamais en file.
4. **Compteurs identiques partout** : tableau de bord, fiche séquence, API et base donnent les mêmes nombres (réponses, envoyés, inscrits) ; une seule définition de comptage par grandeur, écrite dans le code.
5. **Copies aux humains** : chaque vraie réponse reçue par un relais est déposée dans la boîte de Hugo et le libellé Gmail ASD (preuve par UID ou par `message_id`) ; sinon écart.
6. **Classes** : la classe de messagerie d'un contact correspond à son domaine (cas `freemail_fr` du 29/10) ; sinon réclassement proposé.
7. **Stock** : la file ne contient pas de contacts déjà contactés ; le stock par classe annoncé égale le stock réel.

## 4. Les lots (chacun va jusqu'à la prod mesurée)

### Vague 1 (maintenant, lots disjoints)

| Lot | Contenu | Livrable mesurable |
|---|---|---|
| **L0 Socle modulaire** | Geler les fichiers, extraire le câblage Veridian dans `veridian_wiring.go`, registre de migrations séparé, détecteur de fichiers gelés en pre-push, règles écrites dans `docs/veridian/MODULARITE.md` | Le détecteur échoue sur un diff qui touche un fichier gelé (vu échouer) ; `app.go` perd les lignes Veridian |
| **L1 Boîte unifiée** | Contrat 3.2 et 3.3 : table `veridian_mail_message`, consommateur IMAP qui la remplit (rattrapage par UID, humaines et automatiques distinguées, DSN décodés en raison française), reprise de l'historique envoyé depuis `message_history`, calcul des fils, API `mailboxes/messages/threads`, flux d'événements et webhook `message.received`, `messages.send` et `messages.reply`, CLI `mail:*`, console : liste et fiche du message avec rendu HTML assaini en iframe isolé, en-têtes, pièces jointes, DSN expliqué, fil en chronologie illustrée | Une vraie réponse et un vrai rebond de test se lisent en entier par API, CLI et console, dans leur fil avec le mail d'origine ; webhook reçu ; Notifuse arrêté 10 min puis relancé : rien de perdu |
| **L1b Réconciliateur** | Module `internal/veridian/reconcile/` : invariants exécutés à la demande et en continu (voir 3.4), table des écarts, `reconcile.run/report/resolve`, CLI `reconcile:*`, page console « Écarts » (cause, preuve, action en un clic), tests de chaque invariant vus échouer sur une donnée corrompue | Les écarts connus du 27/10 (16 contre 17 réponses, 2 947 contre 2 891 envoyés) expliqués ou corrigés ; un écart provoqué volontairement est détecté puis réparé par l'action proposée |
| **L2 Dossiers natifs** | Entité dossier (arbre, parent, ordre) pour segments, listes, modèles ; routes et CLI (`folders:*`, `move`) ; migration des noms `A / B / C` existants vers l'arbre ; console en arbre déplaçable | Les 33 segments d'animalsd rangés par l'API sans convention de nom ; rien ne contient de `/` par accident |
| **L3 Politique d'envoi et stock** | Présets par classe en code et en données (plafond, cadence, chauffe, seuil de ralentissement), répartition selon le stock (jours de couverture), priorité par campagne (Livrable 4 de l'architecte), routes `stock.overview` (jours par classe, besoins) et `policy.*`, CLI, tuile console | `stock:overview` donne le tableau de `stock-par-classe.py` ; un nouveau workspace envoie sans réglage manuel ; la priorité d'une campagne se règle par API |
| **L4 Qualité et sécurité** | Port des batteries de tests upstream pertinentes (paquets communs : broadcasts, file, templates), tests de caractérisation du noyau Veridian non couvert, correctifs restants du lot 0 (sandbox de l'aperçu e-mail S2, données de contact évaluées par Liquid, rétro-remplissage des `message_data` en clair après sauvegarde restaurable vérifiée), flakiness du runner (timeouts) | Couverture mesurée avant/après ; chaque test ajouté vu échouer sur mutation ; S2 corrigé et vérifié en navigateur |

### Vague 2 (après la livraison de L0 et L1)

| Lot | Contenu |
|---|---|
| **L5 Cockpit « Aujourd'hui »** | Page d'accueil humaine : envoyés/plafond, réponses à traiter, stock en jours, anomalies avec action en français, vocabulaire unique (Profils d'envoi, Séquences), chiffres recoupés. Reprend les 10 défauts du rapport UX |
| **L6 Parité agent et MCP** | Combler les cinq trous bloquants, OpenAPI complète, serveur MCP (outils = routes), commande `ops:digest` (état de la journée en JSON) |
| **L7 Chasse** | Voir section 5 |
| **L8 Montée upstream sélective** | Porter ce qui a de la valeur de v33 à v42 sous licence AGPL compatible (stop-on-reply natif, rapport de clics par lien, corrections A/B, refus nommés), jamais la BSL sans accord |

## 5. Chasse aux prospects (L7, roadmap)

État : l'alimentation passe par des scripts (`alimenter_depuis_odh.py`, `equilibrer_classes.py`, `reclasser_freemail.py`, `stock-par-classe.py`), lancés à la main sur décision de Robert. Plafond mesuré : sur nos activités cibles, ODH n'a que 20 à 40 sociétés dans les classes o2switch, proton, scaleway, Bouygues, Zoho.

Feuille de route :
1. **Outil unique** `asd-chasse` (une CLI testée) qui remplace les scripts : `import`, `classer`, `inscrire`, `rapport`. Lit `stock.overview` pour savoir quoi réapprovisionner.
2. **Qualité avant volume** : vérification SMTP des adresses (sonde `RCPT TO` depuis une IP dédiée, jamais depuis les relais d'envoi) pour faire baisser les rebonds qui ralentissent OVH, Gandi et les passerelles anti-spam.
3. **Sources** : crawl de sites (ODH, fait : 6 510 adresses), registres, annuaires métier ; chaque source porte sa provenance et son taux de rebond mesuré.
4. **Hiérarchisation** : score par valeur réelle (catégorie, proximité, chiffre d'affaires, réponse observée par segment), recalculé avec les réponses des campagnes.
5. **Élargissement hors métier** : décision de Robert au cas par cas, jamais par défaut (réputation d'envoi).

## 6. Livraison : un seul train

Script `~/asd-prospection/notifuse-livrer.sh` : verrou unique (`flock`), rebase sur `origin/veridian`, push, suivi de la CI jusqu'au bout, promotion si le commit ne l'est pas, vérification de `/api/version`. Un lot livre quand le script sort 0 et que le parcours utilisateur a été exécuté sur la prod (captures ou sortie de commande).
Commits : message se terminant par `[risk:low]` pour l'auto-promotion dans le même run (staging E2E bloquant avant la prod). Migration : additive uniquement.

## 7. Garde-fous (loi de Robert)

Aucune dépense (pas de clé de licence, pas de service payant), aucune tâche planifiée nouvelle sans accord, aucun secret dans un dépôt, aucun runner GitHub hébergé, sous-agents en Sonnet. Écritures en prod sur animalsd seulement avec la clé scopée et `NOTIFUSE_STRICT=1`.

## 8. Mesure du sprint

Chaque lot rend : ce qui est en prod (URL, version servie), la preuve (commande ou capture), les tests ajoutés (vus échouer), ce qui reste. Point d'étape à chaque jalon.
