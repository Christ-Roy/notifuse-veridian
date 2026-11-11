# Redirection des réponses de prospects par profil (fiche 78, 04/11/2026)

## Le besoin

Robert, 04/11/2026 : le prospect répond à l'adresse SMTP d'envoi, mais la réponse doit pouvoir
être routée vers l'adresse que l'on veut, typiquement le mail du client lui-même. Réglage PAR
profil d'envoi, vide par défaut : **aucun changement de comportement** pour les espaces existants.

## Le réglage

Champ du fournisseur de l'intégration (JSON du blob, sans migration du blob) :
`veridian_reply_forward_to` = liste d'adresses email, 5 au maximum.

- Validation à l'écriture (crochet Veridian `Workspace.ValidateVeridianReturnIMAPLink`, appelé par
  `workspaces.createIntegration`, `updateIntegration` et `Workspace.Validate` ; `email_provider.go` est
  gelé, il ne reçoit que le champ ; erreur de validation 400) : adresses nues seulement (pas de « Nom <a@b.fr> », pas de virgule, pas de
  saut de ligne), normalisées en minuscules, doublons retirés, **refus** de l'adresse d'envoi du
  profil (expéditeurs et identifiant SMTP) et de plus de 5 adresses. Code :
  `internal/domain/veridian_reply_forward.go`.
- À l'exécution la liste est re-filtrée (`VeridianEffectiveReplyForwardTo`) : un blob écrit hors
  API ne peut pas produire d'envoi vers une adresse invalide ou vers l'adresse d'envoi.

CLI (poser / retirer / voir) :

```bash
notifuse-admin --env staging integrations:cold <ws> --id <profil> --reply-forward-to client@client.fr,autre@client.fr
notifuse-admin --env staging integrations:cold <ws> --id <profil> --reply-forward-off
notifuse-admin --env staging integrations:list <ws>        # champ veridian_reply_forward_to
notifuse-admin --env staging profiles:overview <ws>        # ligne « réponses : copie vers ... »
```

## Ce qui se passe

Dans `VeridianReplyService.ProcessInboundMessage` (stop-on-reply), après le signal `replied`, la
timeline, l'événement Hub et l'exit de séquence, une réponse **humaine** est confiée au
`VeridianReplyForwarder` (`internal/service/veridian_reply_forward.go`) par `Dispatch`, qui rend la
main immédiatement (goroutine bornée à 8 copies en vol, timeout 90 s, panic récupéré).

Le forwarder :

1. re-vérifie que ce n'est ni un rebond/DSN, ni un expéditeur daemon, ni une réponse automatique
   ou un défi (même classifieur que le stop-on-reply) ;
2. rattache la réponse à un de NOS envois (`message_history`, Message-ID cité, sinon dernier envoi
   accepté vers ce contact) pour trouver le **profil** ; pas de profil ou pas de liste = rien ;
3. écarte les boucles (voir plus bas), réserve la copie dans le journal (idempotence + plafond) ;
4. envoie **une** copie par le transport du profil (`EmailService.SendEmail`, SMTP et Gmail) :
   expéditeur = l'identité d'envoi qui a servi, objet `Réponse de <nom ou adresse> : <objet
   d'origine>` (préfixes Re:/TR: retirés), corps = texte de la réponse avec citation conservée
   (HTML converti en texte, 64 Ko au plus) précédé de deux lignes d'explication, `Reply-To` = le
   prospect (son Reply-To s'il en a un, sinon son From), en-tête `X-Veridian-Forwarded: 1` ;
   plusieurs destinataires = un seul message (premier en To, les autres en Cc) ;
5. **une seule tentative immédiate**, puis le journal prend le relais (voir « Fiabilité » plus bas) : un échec
   transitoire est rejoué avec attente exponentielle, un échec définitif reste `failed` avec sa classe.

La copie **ne passe pas** par la file d'envoi ni par les portes cold (réputation, plafonds,
fenêtre) : c'est du courrier transactionnel vers le client, il ne doit pas attendre derrière des
relances ni consommer le plafond commercial. Il ne crée ni `message_history` ni contact.

## Garde-fous (tous testés)

| Garde-fou | Où |
|---|---|
| Jamais pour rebond/DSN, MAILER-DAEMON, auto-réponse, défi | re-vérifié dans `forward()`, indépendamment de l'appelant |
| Jamais si le message entrant porte déjà `X-Veridian-Forwarded` | `forward()` |
| Jamais vers l'adresse d'envoi du profil, ni si l'expéditeur du message EST l'adresse d'envoi | validation + `forward()` |
| Jamais vers le prospect (From, Reply-To, contact d'origine retirés de la liste) ; si l'expéditeur figure dans la liste : pas de copie | `forward()` |
| Idempotent : une réponse = une copie, même si le poller la relit | clé primaire `reply_key` = sha256(workspace, profil, Message-ID du mail entrant, sinon coordonnées IMAP) |
| Plafond par profil (`veridian_reply_forward_daily_cap`, défaut 500, maximum 5000) sur 24 h glissantes, échecs compris, **qui reporte au lieu de perdre** | `Claim` (verrou consultatif par profil) + `LeaseDue` |
| Un échec ne bloque ni la détection, ni l'exit de séquence, ni la lecture IMAP | `Dispatch` asynchrone, aucune erreur remontée |
| Aucune donnée personnelle dans les journaux ni dans la table | logs : workspace, profil, issue ; table : clé hachée, profil, statut, classe d'erreur grossière (`auth`, `timeout`, `refused_local`, `send_error`) |

Un deuxième message du même prospect est bien copié (la clé est par message, pas par contact).

## Fiabilité (11/11/2026, migration V74) : jamais de perte silencieuse

Avant : plafond FIXE de 50 par profil (au-delà, la copie était ignorée en silence : `skip_capped`), « au plus une
fois » (un échec restait `failed` sans rejeu), aucune visibilité. Maintenant :

- **Plafond réglable** : `integrations:cold <ws> --id <profil> --reply-forward-daily-cap N` (1 à 5000, 0 = défaut 500).
  Validé à l'écriture (400 hors borne). C'est un fusible anti-boucle, plus une perte : au-delà du plafond, la copie
  passe en `deferred`, reste dans le journal et part **dans l'ordre d'arrivée** dès qu'une place se libère (une
  nouvelle réponse ne double jamais les plus anciennes reportées). Le dépassement est visible (overview, audit).
- **Rejeu** : le journal garde la copie composée (colonne `payload`) tant qu'elle n'est pas partie ; elle est
  **effacée** dès que la ligne est `sent` ou `failed` (donc des adresses et du texte en base seulement pour les copies
  en attente). Échec **transitoire** (SMTP indisponible, 4xx, timeout, réseau, authentification, inconnu) : statut
  `retry`, rejeu à 1 min, 5 min, 30 min, 2 h, 6 h, 12 h (une tentative + 6 rejeux), puis `failed`. Échec
  **définitif** (5xx hors authentification, refus local) : `failed` tout de suite, avec sa classe (`smtp_5xx`,
  `refused_local`, `no_sender`...). Une redirection retirée entre-temps : les copies en attente passent `failed/disabled`
  (rien ne part). Même `Message-ID` à chaque rejeu, et la clé d'idempotence est inchangée : une réponse = une copie.
- **Mécanisme natif, aucune tâche planifiée externe** : tâche récurrente `veridian_reply_forward_retry` (60 s) dans
  la file de tâches du fork (`tasks`), créée à la demande à la première copie réservée du workspace
  (`VeridianReplyForwardTaskEnsurer`), traitée par `VeridianReplyForwardTaskProcessor`. Elle vit en base : un
  redémarrage ne la perd pas. Un processus qui meurt en plein envoi : le **bail** de 5 min expire et la copie est
  reprise (au pire un doublon d'un même `Message-ID`, jamais une perte). Rafale au-delà de 8 envois en vol : la copie est
  réservée dans le journal et confiée à la tâche (plus de « too many copies in flight, skipped »).
- Statuts : `pending` (à l'envoi, bail), `retry` (rejeu à échéance), `deferred` (plafond), `sent`, `failed`.
  Les lignes `pending`/`retry` sans contenu (créées avant V74) sont closes `failed/no_payload` après 10 min.

## Visibilité

`profiles:overview` (API `emailProfiles.overview`, champ `reply_forward` de chaque profil, même contrat que le reste) :
`enabled`, `daily_cap`, `sent_24h`, `pending`, `retry`, `deferred`, `waiting`, `failed_7d`, `dominant_error_class`,
`oldest_unsent_at`, `oldest_unsent_age_seconds`, `stats_known` (false = journal illisible : jamais présenté comme zéro).
Même rendu dans `notifuse` et `notifuse-admin`. `campaign:audit` (admin et utilisateur, même fonction) contrôle les
profils en rotation : fenêtre d'envoi absente (BLOQUANT, leçon du 03/11), jitter absent ou nul, anti-hash désactivé,
boîte IMAP de retour non liée, copies en attente depuis plus de 2 h ou abandonnées, et « on envoie, personne ne lit les
réponses » (trou du 05/11). Code de sortie 0 GO / 1 NO-GO / 2 non abouti.

## Compteur / journal consultable

Table WORKSPACE `veridian_reply_forward` (migration Veridian **V69**, lot L1d, additive : CREATE TABLE
IF NOT EXISTS d'une table neuve, aucun index secondaire, le tag précédent l'ignore). Colonnes :
`reply_key, profile_id, recipients, status (pending|retry|deferred|sent|failed), error_class, created_at, sent_at`.
**V74** (11/11/2026, additive : quatre `ADD COLUMN IF NOT EXISTS` nullables, métadonnée seule) ajoute `payload` (copie à
envoyer, effacée une fois sent/failed), `attempts`, `next_attempt_at`, `slot_at` (instant où la copie a pris une place
sous le plafond). Le tag précédent ignore ces colonnes.

```sql
-- copies par profil et statut sur 7 jours (base du workspace)
SELECT profile_id, status, count(*) FROM veridian_reply_forward
 WHERE created_at > now() - interval '7 days' GROUP BY 1, 2 ORDER BY 1, 2;
```

Un `pending` ancien de plus de 5 minutes est repris par la tâche de rejeu (bail expiré). Compteurs et âge de la plus
ancienne copie non envoyée : `profiles:overview`.

## Ce qui reste hors périmètre

- Pas d'écran console (le réglage passe par le CLI et l'API `updateIntegration`).
- Le journal n'est jamais purgé (lignes `sent`/`failed` sans contenu, quelques dizaines d'octets chacune).
- Profils non SMTP (SES, SendGrid...) : pas de copie (le texte brut n'y est pas supporté), ignoré
  sans erreur.
- Réponses qui arrivent par la boîte unifiée sans passer par le poller de stop-on-reply : non couvertes.

## Tests

`internal/domain/veridian_reply_forward_test.go` (validation), `internal/repository/
veridian_reply_forward_postgres_test.go` (SQL), `internal/service/veridian_reply_forward_test.go`
(copie, garde-fous, idempotence, plafond, échec non bloquant, branchement dans le service),
`internal/service/smtp_service_test.go` (en-tête `X-Veridian-*` en liste blanche, porté par le contexte `domain.WithVeridianExtraHeaders`),
`internal/migrations/veridian/v69_reply_forward_test.go`. Preuve de détection : mutations
volontaires du code (garde auto/DSN, garde `X-Veridian-Forwarded`, idempotence, plafond, gestion
de l'échec) chacune rattrapée par un test rouge. Lot de fiabilité : `veridian_reply_forward_task_test.go`,
`v74_reply_forward_retry_test.go`, et 13 mutations Go (rejeu, report, idempotence, tâche native, backoff, effacement du
contenu, classification, bail, récurrence, bornes du plafond, redirection retirée) + 11 mutations Python (chaque contrôle
d'audit) toutes rattrapées. CLI : `tests/test_reply_forward_robust.py`, `tests/test_campaign_audit_rotation.py`.
