# Redirection des réponses de prospects par profil (fiche 78, 04/11/2026)

## Le besoin

Robert, 04/11/2026 : le prospect répond à l'adresse SMTP d'envoi, mais la réponse doit pouvoir
être routée vers l'adresse que l'on veut, typiquement le mail du client lui-même. Réglage PAR
profil d'envoi, vide par défaut : **aucun changement de comportement** pour les espaces existants.

## Le réglage

Champ du fournisseur de l'intégration (JSON du blob, sans migration du blob) :
`veridian_reply_forward_to` = liste d'adresses email, 5 au maximum.

- Validation à l'écriture (`EmailProvider.Validate`, donc `workspaces.createIntegration` et
  `updateIntegration`) : adresses nues seulement (pas de « Nom <a@b.fr> », pas de virgule, pas de
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
5. une relance après 5 s en cas d'échec, puis journalise `failed` (sans rejouer).

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
| Plafond : 50 copies par profil sur 24 h glissantes, échecs compris | `Claim` (INSERT conditionnel) |
| Un échec ne bloque ni la détection, ni l'exit de séquence, ni la lecture IMAP | `Dispatch` asynchrone, aucune erreur remontée |
| Aucune donnée personnelle dans les journaux ni dans la table | logs : workspace, profil, issue ; table : clé hachée, profil, statut, classe d'erreur grossière (`auth`, `timeout`, `refused_local`, `send_error`) |

Sémantique « au plus une fois » assumée : la réservation précède l'envoi. Une copie dont l'envoi
échoue deux fois reste `failed` dans le journal et n'est pas rejouée toute seule (pas de boucle).
Un deuxième message du même prospect, lui, est bien copié (la clé est par message, pas par contact).

## Compteur / journal consultable

Table WORKSPACE `veridian_reply_forward` (migration Veridian **V69**, lot L1d, additive : CREATE TABLE
IF NOT EXISTS d'une table neuve, aucun index secondaire, le tag précédent l'ignore). Colonnes :
`reply_key, profile_id, recipients, status (pending|sent|failed), error_class, created_at, sent_at`.

```sql
-- copies par profil et statut sur 7 jours (base du workspace)
SELECT profile_id, status, count(*) FROM veridian_reply_forward
 WHERE created_at > now() - interval '7 days' GROUP BY 1, 2 ORDER BY 1, 2;
```

Un `pending` ancien de plus de quelques minutes = processus arrêté pendant l'envoi (à regarder).
Le dépôt expose aussi `Stats` (`domain.VeridianReplyForwardRepository`) pour un futur écran.

## Ce qui reste hors périmètre

- Pas d'écran console (le réglage passe par le CLI et l'API `updateIntegration`).
- Profils non SMTP (SES, SendGrid...) : pas de copie (le texte brut n'y est pas supporté), ignoré
  sans erreur.
- Réponses qui arrivent par la boîte unifiée sans passer par le poller de stop-on-reply : non couvertes.

## Tests

`internal/domain/veridian_reply_forward_test.go` (validation), `internal/repository/
veridian_reply_forward_postgres_test.go` (SQL), `internal/service/veridian_reply_forward_test.go`
(copie, garde-fous, idempotence, plafond, échec non bloquant, branchement dans le service),
`internal/service/smtp_service_test.go` (en-tête `X-Veridian-*` liste blanche),
`internal/migrations/veridian/v69_reply_forward_test.go`. Preuve de détection : mutations
volontaires du code (garde auto/DSN, garde `X-Veridian-Forwarded`, idempotence, plafond, gestion
de l'échec) chacune rattrapée par un test rouge.
